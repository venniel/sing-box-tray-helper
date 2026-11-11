package main

import (
	"os"
	"path/filepath"
	"runtime"
)

const (
	appTitle     = "sing-box tray helper"
	appShortName = "sing-box"

	// Outwait the old instance's graceful core stop (10s budget) plus its
	// output drain on restart.
	restartHandoffTimeoutMs = 15000
)

func main() {
	runtime.LockOSThread()

	enableDPIAwareness()
	initCommonControls()
	if oleInitialize() {
		defer pOleUninitialize.Call()
	}

	flags := parseAppFlags(os.Args[1:])

	if !acquireSingleInstance("STH_SingleInstance_Mutex", flags.has(afRestart), restartHandoffTimeoutMs) {
		messageBox("Another instance of this application is already running.", appTitle, mbIconError)
		return
	}

	firstRun := !fileExists(programDir() + optionsFilename)
	var c *controller

	for {
		if firstRun {
			if showSettingsModal(true, "") != idOK {
				return
			}
			firstRun = false
		}

		var err error
		c, err = newController(flags)
		if err == nil {
			break
		}

		if showSettingsModal(false, err.Error()) != idOK {
			messageBox(err.Error(), appTitle, mbIconError)
			return
		}
	}

	if c.needsElevation() {
		if launchSelf(flagsToCmdLine(appFlags(1<<afTun|1<<afRestart)), 0, true) {
			c.destroy()
			return
		}
	}

	// Start the core only after the elevation decision so a relaunch does not
	// spawn a throwaway instance first.
	c.startCore(c.wantsTun)

	ui, err := newMainUI(c, flags)
	if err != nil {
		messageBox(err.Error(), appTitle, mbIconError)
		c.destroy()
		return
	}

	c.setDispatch(ui.post)
	ui.initController()
	ui.run()
	ui.destroyWindow()
	c.destroy()
}

func programDir() string {
	return includeTrailingPathDelimiter(filepath.Dir(currentExecutablePath()))
}

func enableDPIAwareness() {
	if r, _, _ := pSetProcessDpiAwareCtx.Call(dpiCtxPerMonV2); r != 0 {
		return
	}
	pSetProcessDPIAware.Call()
}

func oleInitialize() bool {
	r, _, _ := pOleInitialize.Call(0)
	return int32(r) >= 0
}
