package main

import "strings"

const (
	idMenuRestart      = 1
	idMenuAutostart    = 2
	idMenuHomepage     = 3
	idMenuDashboard    = 4
	idMenuQuit         = 5
	idMenuTun          = 6
	idMenuSystemProxy  = 7
	idMenuModeBase     = 100
	idMenuSelectorBase = 10000
)

type selectorAction struct {
	SelectorIndex int
	OutboundIndex int
}

func (u *mainUI) showTrayMenu() {
	u.c.refreshSelectorStates()

	hmenu, _ := u.buildMenu()
	if hmenu == 0 {
		return
	}

	pt, _ := getCursorPos()
	pSetForegroundWindow.Call(u.hwnd)
	cmd, _, _ := pTrackPopupMenuEx.Call(hmenu,
		tpmRightBtn|tpmReturnCmd,
		uintptr(pt.X), uintptr(pt.Y),
		u.hwnd, 0)
	pDestroyMenu.Call(hmenu)
	postMessage(u.hwnd, wmNull, 0, 0)

	if cmd != 0 {
		u.executeMenuCommand(uint32(cmd))
	}
}

func (u *mainUI) buildMenu() (uintptr, bool) {
	menu, _, _ := pCreatePopupMenu.Call()
	if menu == 0 {
		return 0, false
	}

	u.menuActions = map[uint32]selectorAction{}
	u.menuModes = nil

	if u.closePending {
		appendMenu(menu, mfString, idMenuQuit, "退出")
		return menu, true
	}

	ctl := u.c

	extras, _, _ := pCreatePopupMenu.Call()
	appendMenu(extras, mfString, idMenuRestart, "重启")

	autostartFlags := uintptr(mfString)
	if u.autostartBusy || u.autostartState == asUnknown {
		autostartFlags |= mfGrayed
	}
	if u.autostartState == asEnabled {
		autostartFlags |= mfChecked
	}
	appendMenu(extras, autostartFlags, idMenuAutostart, "开机自启")
	appendMenu(extras, mfString, idMenuHomepage, "项目主页")
	appendSubMenu(menu, extras, "更多")

	u.appendModeMenu(menu)
	u.appendNodeMenu(menu)

	if ctl.sbConfig.HasTunInbound {
		flags := uintptr(mfString)
		if u.isTunActive {
			flags |= mfChecked
		}
		if u.tunBusy {
			flags |= mfGrayed
		}
		appendMenu(menu, flags, idMenuTun, "TUN 模式")
	}

	proxyFlags := uintptr(mfString)
	if u.isSystemProxyActive {
		proxyFlags |= mfChecked
	}
	appendMenu(menu, proxyFlags, idMenuSystemProxy, "系统代理")

	appendMenu(menu, mfString, idMenuDashboard, "面板")

	appendMenu(menu, mfString, idMenuQuit, "退出")
	return menu, true
}

func (u *mainUI) appendModeMenu(menu uintptr) {
	modes := u.c.clashModes
	if len(modes) == 0 {
		return
	}
	if len(modes) >= idMenuSelectorBase-idMenuModeBase {
		modes = modes[:idMenuSelectorBase-idMenuModeBase]
	}
	u.menuModes = append([]string(nil), modes...)

	sub, _, _ := pCreatePopupMenu.Call()
	if sub == 0 {
		return
	}
	for i, mode := range modes {
		flags := uintptr(mfString)
		if strings.EqualFold(mode, u.c.clashMode) {
			flags |= mfChecked
		}
		appendMenu(sub, flags, uintptr(idMenuModeBase+i), mode)
	}
	appendSubMenu(menu, sub, "模式")
}

func (u *mainUI) appendNodeMenu(menu uintptr) {
	selectors := u.c.selectors
	count := len(selectors)
	if count < 1 || count > 50 {
		return
	}

	isNested := false
	switch u.c.options.SelectorMenuLayout {
	case smlFlat:
		isNested = false
	case smlNested:
		isNested = true
	default:
		flatRows := 2 * count
		for _, selector := range selectors {
			flatRows += len(selector.Outbounds)
		}
		isNested = flatRows > 20
	}

	disabled := map[int]bool{}
	for _, action := range u.pending {
		disabled[action.SelectorIndex*1000+action.OutboundIndex] = true
	}

	sub, _, _ := pCreatePopupMenu.Call()
	if sub == 0 {
		return
	}

	nextID := idMenuSelectorBase
	for selectorIdx, selector := range selectors {
		var selectorMenu uintptr
		if isNested {
			selectorMenu, _, _ = pCreatePopupMenu.Call()
			if selectorMenu == 0 {
				continue
			}
		} else {
			appendMenu(sub, mfString|mfDisabled, 0, selector.Name)
		}

		checkedIndex := selector.DefaultIndex
		if now, ok := u.c.selectorNow(selector.Name); ok {
			for j, outbound := range selector.Outbounds {
				if outbound == now {
					checkedIndex = j
					break
				}
			}
		}

		for outboundIdx, outbound := range selector.Outbounds {
			flags := uintptr(mfString)
			if outboundIdx == checkedIndex {
				flags |= mfChecked
			}
			if disabled[selectorIdx*1000+outboundIdx] {
				flags |= mfGrayed
			}
			id := uint32(nextID)
			nextID++
			u.menuActions[id] = selectorAction{SelectorIndex: selectorIdx, OutboundIndex: outboundIdx}
			if isNested {
				appendMenu(selectorMenu, flags, uintptr(id), outbound)
			} else {
				appendMenu(sub, flags, uintptr(id), outbound)
			}
		}

		if isNested {
			appendSubMenu(sub, selectorMenu, selector.Name)
		} else if selectorIdx < count-1 {
			appendMenu(sub, mfSeparator, 0, "")
		}
	}

	appendSubMenu(menu, sub, "节点")
}

func (u *mainUI) executeMenuCommand(cmd uint32) {
	switch cmd {
	case idMenuRestart:
		u.restartApp()
		return
	case idMenuAutostart:
		u.toggleAutostart()
		return
	case idMenuHomepage:
		shellOpen("https://github.com/sagernet/sing-box")
		return
	case idMenuDashboard:
		shellOpen(u.c.sbConfig.ClashAPI.DashboardURL())
		return
	case idMenuQuit:
		u.requestClose()
		return
	case idMenuTun:
		u.toggleTun(!u.isTunActive)
		return
	case idMenuSystemProxy:
		u.toggleSystemProxy(!u.isSystemProxyActive, true, true)
		return
	}

	if action, ok := u.menuActions[cmd]; ok {
		u.editSelector(action)
		return
	}
	if cmd >= idMenuModeBase && int(cmd) < idMenuModeBase+len(u.menuModes) {
		u.selectMode(u.menuModes[cmd-idMenuModeBase])
	}
}
