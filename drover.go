package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
)

type droverEventKind int

const (
	dekError droverEventKind = iota
	dekRunning
	dekCoreEvent
	dekClashModes
)

type droverEvent struct {
	Kind      droverEventKind
	Msg       string
	CoreEvent coreEvent
}

type drover struct {
	supervisor  *coreSupervisor
	logger      *logger
	optionsFile string
	options     droverOptions
	sbConfig    singBoxConfig
	selectors   []configSelector

	currentProcessDir string
	isElevated        bool
	isTunActive       bool
	wantsTun          bool
	clashMode         string
	clashModes        []string

	selectorStates map[string]string

	mu                sync.Mutex
	dispatch          func(func())
	pendingOps        []func()
	onEvent           func(droverEvent)
	pendingEvents     []droverEvent
	notifyHwnd        uintptr
	shutdownRequested bool
	shutdownCompleted bool
	destroying        bool
}

func newDrover(flags appFlags) (*drover, error) {
	d := &drover{}
	d.currentProcessDir = includeTrailingPathDelimiter(filepath.Dir(currentExecutablePath()))
	d.optionsFile = d.currentProcessDir + optionsFilename
	d.options = loadOptions(d.optionsFile)
	d.logger = newLogger(d.options.LogFile)

	fail := func(err error) (*drover, error) {
		d.logger.close()
		return nil, err
	}

	jsonText, err := readConfigText(d.options.SBConfigFile)
	if err != nil {
		return fail(err)
	}
	cfg, err := readSingBoxConfig(jsonText)
	if err != nil {
		return fail(err)
	}
	if err := checkSingBoxConfig(cfg); err != nil {
		return fail(err)
	}
	d.sbConfig = cfg
	d.selectors = append([]configSelector(nil), cfg.Selectors...)

	d.wantsTun = d.options.TunStartMode == tsmOn
	if flags.has(afTun) {
		d.wantsTun = true
	}

	d.isElevated = isProcessElevated()

	corePath := d.options.SBExe
	if !fileExists(corePath) {
		return fail(errors.New("sing-box executable not found."))
	}

	d.supervisor = newCoreSupervisor(corePath, d.logger, cfg.ClashAPI, d.dispatchCoreEvent)

	return d, nil
}

func (d *drover) setDispatch(dispatch func(func())) {
	d.mu.Lock()
	d.dispatch = dispatch
	ops := d.pendingOps
	d.pendingOps = nil
	d.mu.Unlock()

	for _, op := range ops {
		dispatch(op)
	}
}

func (d *drover) setOnEvent(handler func(droverEvent)) {
	d.mu.Lock()
	d.onEvent = handler
	events := d.pendingEvents
	d.pendingEvents = nil
	d.mu.Unlock()

	for _, ev := range events {
		handler(ev)
	}
}

func (d *drover) postToUI(fn func()) {
	d.mu.Lock()
	if d.dispatch != nil {
		dispatch := d.dispatch
		d.mu.Unlock()
		dispatch(fn)
		return
	}
	d.pendingOps = append(d.pendingOps, fn)
	d.mu.Unlock()
}

func (d *drover) dispatchCoreEvent(ev coreEvent) {
	d.postToUI(func() { d.handleCoreEvent(ev) })
}

func (d *drover) handleCoreEvent(ev coreEvent) {
	d.mu.Lock()
	stopping := d.destroying || d.shutdownRequested
	d.mu.Unlock()
	if stopping {
		return
	}

	switch ev.Kind {
	case cekState:
		switch ev.State {
		case csRunning:
			d.notifyEvent(droverEvent{Kind: dekRunning})
		case csFailed:
			d.notifyEvent(droverEvent{Kind: dekError, Msg: ev.Msg})
		}

	case cekError:
		d.notifyEvent(droverEvent{Kind: dekError, Msg: ev.Msg})

	case cekAPIReady:
		d.refreshSelectorStates()

	case cekConfigModes, cekModeDone:
		d.clashMode = ev.Mode
		if len(ev.Modes) > 0 {
			d.clashModes = ev.Modes
		}
		d.notifyEvent(droverEvent{Kind: dekClashModes})

	case cekSelectorDone:
		d.notifyEvent(droverEvent{Kind: dekCoreEvent, CoreEvent: ev})
	}
}

func (d *drover) notifyEvent(ev droverEvent) {
	d.mu.Lock()
	if d.onEvent == nil {
		d.pendingEvents = append(d.pendingEvents, ev)
		d.mu.Unlock()
		return
	}
	handler := d.onEvent
	d.mu.Unlock()
	handler(ev)
}

func (d *drover) setNotifyHandle(hwnd uintptr) {
	d.mu.Lock()
	d.notifyHwnd = hwnd
	d.mu.Unlock()
}

func (d *drover) setSelectorMenuLayout(layout selectorMenuLayout) {
	d.options.SelectorMenuLayout = layout
}

func (d *drover) needsElevation() bool {
	return d.wantsTun && d.sbConfig.HasTunInbound && !d.isElevated
}

func (d *drover) systemProxyEnabled() bool {
	return d.options.SystemProxyAuto
}

func (d *drover) storeSystemProxy(value bool) {
	d.options.SystemProxyAuto = value
	if err := saveSystemProxyState(d.optionsFile, value); err != nil {
		d.log("Failed to persist system proxy state. " + err.Error())
	}
}

func (d *drover) storeTun(value bool) {
	d.wantsTun = value
	d.options.TunStartMode = tsmOff
	if value {
		d.options.TunStartMode = tsmOn
	}
	if err := saveTunState(d.optionsFile, value); err != nil {
		d.log("Failed to persist TUN state. " + err.Error())
	}
}

func (d *drover) editSelector(selectorIdx, outboundIdx, requestID int) bool {
	if selectorIdx < 0 || selectorIdx >= len(d.selectors) {
		return false
	}
	selector := d.selectors[selectorIdx]
	if outboundIdx < 0 || outboundIdx >= len(selector.Outbounds) {
		return false
	}

	d.selectors[selectorIdx].DefaultIndex = outboundIdx
	if d.selectorStates == nil {
		d.selectorStates = map[string]string{}
	}
	d.selectorStates[selector.Name] = selector.Outbounds[outboundIdx]

	task := selectorTask{Name: selector.Name, Value: selector.Outbounds[outboundIdx]}
	return d.supervisor.requestSetSelectors([]selectorTask{task}, requestID)
}

// refreshSelectorStates asks the core for the active outbound of every
// selector. It runs synchronously before the tray menu is built so the first
// open already shows changes made elsewhere (for example via the dashboard);
// on failure the cached values are kept.
func (d *drover) refreshSelectorStates() {
	if len(d.selectors) == 0 {
		return
	}
	names := make([]string, 0, len(d.selectors))
	for _, selector := range d.selectors {
		names = append(names, selector.Name)
	}

	states := d.supervisor.querySelectorStates(names)
	if states == nil {
		return
	}
	if d.selectorStates == nil {
		d.selectorStates = map[string]string{}
	}
	for name, now := range states {
		d.selectorStates[name] = now
	}
}

func (d *drover) selectorNow(name string) (string, bool) {
	value, ok := d.selectorStates[name]
	return value, ok
}

func (d *drover) setClashMode(mode string) bool {
	return d.supervisor.requestSetMode(mode)
}

func (d *drover) enableSystemProxy() bool {
	return enableSystemProxy(d.sbConfig.ProxyHost, d.sbConfig.ProxyPort)
}

func (d *drover) disableSystemProxy() bool {
	return disableSystemProxy()
}

func (d *drover) startCore(useTun bool) {
	if !d.canUseTun() {
		useTun = false
	}
	d.isTunActive = useTun

	if useTun {
		if removed := removeStaleWintunAdapters(); removed > 0 {
			d.log(fmt.Sprintf("Removed %d stale Wintun adapter(s).", removed))
		}
	}

	configJSON := d.sbConfig.JSONWithoutTun
	if useTun {
		configJSON = d.sbConfig.JSONWithTun
	}
	d.supervisor.requestStart(configJSON)
}

func (d *drover) canUseTun() bool {
	return d.sbConfig.HasTunInbound && d.isElevated
}

func (d *drover) shutdown() bool {
	d.mu.Lock()
	if d.shutdownCompleted {
		d.mu.Unlock()
		return true
	}
	needRequest := !d.shutdownRequested
	d.shutdownRequested = true
	d.mu.Unlock()

	if needRequest {
		d.requestShutdownWorkers()
	}

	d.mu.Lock()
	completed := d.shutdownCompleted
	d.mu.Unlock()
	return completed
}

func (d *drover) stopForShutdown() {
	d.mu.Lock()
	if d.shutdownCompleted {
		d.mu.Unlock()
		return
	}
	d.shutdownRequested = true
	d.mu.Unlock()

	if d.supervisor != nil {
		d.supervisor.stop()
	}
	d.completeShutdown()
}

func (d *drover) requestShutdownWorkers() {
	go func() {
		if d.supervisor != nil {
			d.supervisor.stop()
		}
		d.completeShutdown()
	}()
}

func (d *drover) completeShutdown() {
	d.mu.Lock()
	if d.shutdownCompleted {
		d.mu.Unlock()
		return
	}
	d.shutdownCompleted = true
	d.mu.Unlock()

	if d.logger != nil {
		d.logger.close()
	}
	d.postCanClose()
}

func (d *drover) postCanClose() {
	d.mu.Lock()
	hwnd := d.notifyHwnd
	d.mu.Unlock()

	if hwnd == 0 {
		return
	}
	if r, _, _ := pIsWindow.Call(hwnd); r == 0 {
		return
	}
	postMessage(hwnd, wmDroverCanClose, 0, 0)
}

func (d *drover) destroy() {
	d.mu.Lock()
	d.destroying = true
	alreadyRequested := d.shutdownRequested
	d.mu.Unlock()

	// Shutdown workers are already running (normal exit or the close
	// watchdog); waiting here could hang the process indefinitely.
	if alreadyRequested {
		return
	}
	d.stopForShutdown()
}

func (d *drover) log(message string) {
	d.logger.log("Drover", message)
}
