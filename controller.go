package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
)

type controllerEventKind int

const (
	cevError controllerEventKind = iota
	cevRunning
	cevCoreEvent
	cevClashModes
)

type controllerEvent struct {
	Kind      controllerEventKind
	Msg       string
	CoreEvent coreEvent
}

type controller struct {
	supervisor  *coreSupervisor
	logger      *logger
	optionsFile string
	options     appOptions
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
	onEvent           func(controllerEvent)
	pendingEvents     []controllerEvent
	notifyHwnd        uintptr
	shutdownRequested bool
	shutdownCompleted bool
	destroying        bool
}

func newController(flags appFlags) (*controller, error) {
	c := &controller{}
	c.currentProcessDir = includeTrailingPathDelimiter(filepath.Dir(currentExecutablePath()))
	c.optionsFile = c.currentProcessDir + optionsFilename
	c.options = loadOptions(c.optionsFile)
	c.logger = newLogger(c.options.LogFile)

	fail := func(err error) (*controller, error) {
		c.logger.close()
		return nil, err
	}

	jsonText, err := readConfigText(c.options.SBConfigFile)
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
	c.sbConfig = cfg
	c.selectors = append([]configSelector(nil), cfg.Selectors...)

	c.wantsTun = c.options.TunStartMode == tsmOn
	if flags.has(afTun) {
		c.wantsTun = true
	}

	c.isElevated = isProcessElevated()

	corePath := c.options.SBExe
	if !fileExists(corePath) {
		return fail(errors.New("sing-box executable not found."))
	}

	c.supervisor = newCoreSupervisor(corePath, c.logger, cfg.ClashAPI, c.dispatchCoreEvent)

	return c, nil
}

func (c *controller) setDispatch(dispatch func(func())) {
	c.mu.Lock()
	c.dispatch = dispatch
	ops := c.pendingOps
	c.pendingOps = nil
	c.mu.Unlock()

	for _, op := range ops {
		dispatch(op)
	}
}

func (c *controller) setOnEvent(handler func(controllerEvent)) {
	c.mu.Lock()
	c.onEvent = handler
	events := c.pendingEvents
	c.pendingEvents = nil
	c.mu.Unlock()

	for _, ev := range events {
		handler(ev)
	}
}

func (c *controller) postToUI(fn func()) {
	c.mu.Lock()
	if c.dispatch != nil {
		dispatch := c.dispatch
		c.mu.Unlock()
		dispatch(fn)
		return
	}
	c.pendingOps = append(c.pendingOps, fn)
	c.mu.Unlock()
}

func (c *controller) dispatchCoreEvent(ev coreEvent) {
	c.postToUI(func() { c.handleCoreEvent(ev) })
}

func (c *controller) handleCoreEvent(ev coreEvent) {
	c.mu.Lock()
	stopping := c.destroying || c.shutdownRequested
	c.mu.Unlock()
	if stopping {
		return
	}

	switch ev.Kind {
	case cekState:
		switch ev.State {
		case csRunning:
			c.notifyEvent(controllerEvent{Kind: cevRunning})
		case csFailed:
			c.notifyEvent(controllerEvent{Kind: cevError, Msg: ev.Msg})
		}

	case cekError:
		c.notifyEvent(controllerEvent{Kind: cevError, Msg: ev.Msg})

	case cekAPIReady:
		c.refreshSelectorStates()

	case cekConfigModes, cekModeDone:
		c.clashMode = ev.Mode
		if len(ev.Modes) > 0 {
			c.clashModes = ev.Modes
		}
		c.notifyEvent(controllerEvent{Kind: cevClashModes})

	case cekSelectorDone:
		c.notifyEvent(controllerEvent{Kind: cevCoreEvent, CoreEvent: ev})
	}
}

func (c *controller) notifyEvent(ev controllerEvent) {
	c.mu.Lock()
	if c.onEvent == nil {
		c.pendingEvents = append(c.pendingEvents, ev)
		c.mu.Unlock()
		return
	}
	handler := c.onEvent
	c.mu.Unlock()
	handler(ev)
}

func (c *controller) setNotifyHandle(hwnd uintptr) {
	c.mu.Lock()
	c.notifyHwnd = hwnd
	c.mu.Unlock()
}

func (c *controller) setSelectorMenuLayout(layout selectorMenuLayout) {
	c.options.SelectorMenuLayout = layout
}

func (c *controller) needsElevation() bool {
	return c.wantsTun && c.sbConfig.HasTunInbound && !c.isElevated
}

func (c *controller) systemProxyEnabled() bool {
	return c.options.SystemProxyAuto
}

func (c *controller) storeSystemProxy(value bool) {
	c.options.SystemProxyAuto = value
	if err := saveSystemProxyState(c.optionsFile, value); err != nil {
		c.log("Failed to persist system proxy state. " + err.Error())
	}
}

func (c *controller) storeTun(value bool) {
	c.wantsTun = value
	c.options.TunStartMode = tsmOff
	if value {
		c.options.TunStartMode = tsmOn
	}
	if err := saveTunState(c.optionsFile, value); err != nil {
		c.log("Failed to persist TUN state. " + err.Error())
	}
}

func (c *controller) editSelector(selectorIdx, outboundIdx, requestID int) bool {
	if selectorIdx < 0 || selectorIdx >= len(c.selectors) {
		return false
	}
	selector := c.selectors[selectorIdx]
	if outboundIdx < 0 || outboundIdx >= len(selector.Outbounds) {
		return false
	}

	c.selectors[selectorIdx].DefaultIndex = outboundIdx
	if c.selectorStates == nil {
		c.selectorStates = map[string]string{}
	}
	c.selectorStates[selector.Name] = selector.Outbounds[outboundIdx]

	task := selectorTask{Name: selector.Name, Value: selector.Outbounds[outboundIdx]}
	return c.supervisor.requestSetSelectors([]selectorTask{task}, requestID)
}

// refreshSelectorStates asks the core for the active outbound of every
// selector. It runs synchronously before the tray menu is built so the first
// open already shows changes made elsewhere (for example via the dashboard);
// on failure the cached values are kept.
func (c *controller) refreshSelectorStates() {
	if len(c.selectors) == 0 {
		return
	}
	names := make([]string, 0, len(c.selectors))
	for _, selector := range c.selectors {
		names = append(names, selector.Name)
	}

	states := c.supervisor.querySelectorStates(names)
	if states == nil {
		return
	}
	if c.selectorStates == nil {
		c.selectorStates = map[string]string{}
	}
	for name, now := range states {
		c.selectorStates[name] = now
	}
}

func (c *controller) selectorNow(name string) (string, bool) {
	value, ok := c.selectorStates[name]
	return value, ok
}

func (c *controller) setClashMode(mode string) bool {
	return c.supervisor.requestSetMode(mode)
}

func (c *controller) enableSystemProxy() bool {
	return enableSystemProxy(c.sbConfig.ProxyHost, c.sbConfig.ProxyPort)
}

func (c *controller) disableSystemProxy() bool {
	return disableSystemProxy()
}

func (c *controller) startCore(useTun bool) {
	if !c.canUseTun() {
		useTun = false
	}
	c.isTunActive = useTun

	if useTun {
		if removed := removeStaleWintunAdapters(); removed > 0 {
			c.log(fmt.Sprintf("Removed %d stale Wintun adapter(s).", removed))
		}
	}

	configJSON := c.sbConfig.JSONWithoutTun
	if useTun {
		configJSON = c.sbConfig.JSONWithTun
	}
	c.supervisor.requestStart(configJSON)
}

func (c *controller) canUseTun() bool {
	return c.sbConfig.HasTunInbound && c.isElevated
}

func (c *controller) shutdown() bool {
	c.mu.Lock()
	if c.shutdownCompleted {
		c.mu.Unlock()
		return true
	}
	needRequest := !c.shutdownRequested
	c.shutdownRequested = true
	c.mu.Unlock()

	if needRequest {
		c.requestShutdownWorkers()
	}

	c.mu.Lock()
	completed := c.shutdownCompleted
	c.mu.Unlock()
	return completed
}

func (c *controller) stopForShutdown() {
	c.mu.Lock()
	if c.shutdownCompleted {
		c.mu.Unlock()
		return
	}
	c.shutdownRequested = true
	c.mu.Unlock()

	if c.supervisor != nil {
		c.supervisor.stop()
	}
	c.completeShutdown()
}

func (c *controller) requestShutdownWorkers() {
	go func() {
		if c.supervisor != nil {
			c.supervisor.stop()
		}
		c.completeShutdown()
	}()
}

func (c *controller) completeShutdown() {
	c.mu.Lock()
	if c.shutdownCompleted {
		c.mu.Unlock()
		return
	}
	c.shutdownCompleted = true
	c.mu.Unlock()

	if c.logger != nil {
		c.logger.close()
	}
	c.postCanClose()
}

func (c *controller) postCanClose() {
	c.mu.Lock()
	hwnd := c.notifyHwnd
	c.mu.Unlock()

	if hwnd == 0 {
		return
	}
	if r, _, _ := pIsWindow.Call(hwnd); r == 0 {
		return
	}
	postMessage(hwnd, wmControllerCanClose, 0, 0)
}

func (c *controller) destroy() {
	c.mu.Lock()
	c.destroying = true
	alreadyRequested := c.shutdownRequested
	c.mu.Unlock()

	// Shutdown workers are already running (normal exit or the close
	// watchdog); waiting here could hang the process indefinitely.
	if alreadyRequested {
		return
	}
	c.stopForShutdown()
}

func (c *controller) log(message string) {
	c.logger.log("App", message)
}
