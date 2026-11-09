# sing-box tray helper

A lightweight tray controller for Windows that runs the official `sing-box` core in the background and exposes a
small, native control surface: system proxy, TUN mode, Clash mode switching, selector switching and a link to the
Clash dashboard.

The goal is to keep the full power of a normal sing-box config while adding only the Windows-specific glue, without
becoming another all-in-one GUI client.

- Uses the official `sing-box.exe` — nothing is embedded, nothing is bundled
- One JSON config, used as-is
- Native Win32 controls (no GUI toolkit) with explicit accessible names, so screen readers such as NVDA and
  争渡读屏 can navigate the tray menu and settings dialog
- Small idle footprint: no periodic HTTP polling after startup; the Clash API is contacted at startup, when the
  menu is opened and when you switch something

## Features

- Tray icon reflects the current state: TUN active, system proxy enabled, or idle
- Left-click the tray icon to open the settings dialog
- Right-click for the context menu:
    - **更多** (More) — restart, autostart, project page
    - **模式** (Mode) — Clash mode switcher, populated from the Clash API
    - **节点** (Nodes) — selector outbounds; the outbound currently used by the core is marked, even when it was
      changed from outside this program (for example from the dashboard). The program never pushes a selection to
      the core at startup; persistence is left to the core (see below).
    - **TUN 模式** (TUN mode) — available when the config contains a `tun` inbound; asks for elevation when needed
    - **系统代理** (System proxy) — toggles the Windows proxy settings
    - **面板** (Dashboard) — opens the dashboard served by the core when `external_ui` is configured, otherwise the
      hosted yacd panel
    - **退出** (Quit) — gracefully stops the core and exits
- The system proxy is restored on the next launch when it was left enabled, and is disabled on normal exit
- TUN state is remembered across restarts; enabling TUN relaunches the program elevated (UAC)
- Single instance per user session; starting a second copy is refused
- Optional autostart through a scheduled task

## Requirements

- Windows 10 or later (64-bit)
- The official `sing-box` for Windows (`sing-box.exe`), downloaded from the upstream project:
  <https://github.com/SagerNet/sing-box/releases>
- For TUN mode, the `wintun.dll` that ships with the official Windows archive must be next to `sing-box.exe`
- A sing-box config that satisfies the requirements below

## Getting started

1. Download `STH.exe` from the [latest release](../../releases/latest), or build it yourself:

   ```sh
   go build -ldflags "-H=windowsgui" -trimpath -o STH.exe .
   ```

2. Put `STH.exe` in any folder. The settings dialog creates `sth.ini` when you save for the first time.
3. Put `sing-box.exe` next to `STH.exe`, or point `sb-exe` at the executable's full path.
4. Run `STH.exe`. On the first launch the settings dialog opens; choose the core executable and the config
   file. After saving, a tray icon appears and the core starts.

The tray menu and the settings dialog are in Chinese.

## sing-box configuration requirements

### Required: mixed inbound

The program always needs a `mixed` inbound: it reads `listen` and `listen_port` to configure the Windows system proxy
and refuses to start without one.

```jsonc
{
  "inbounds": [
    {
      "type": "mixed",
      "tag": "mixed-in", // any tag, not used by the program
      "listen": "127.0.0.1",
      "listen_port": 1080
    }
  ]
}
```

### Optional: tun inbound

If the config contains an inbound of type `tun`, the TUN menu item appears. Without it, TUN mode is unavailable.

```jsonc
{
  "inbounds": [
    {
      "type": "tun",
      "tag": "tun-in",
      "address": ["172.19.0.1/30"],
      "auto_route": true
    }
  ]
}
```

When TUN is disabled, the program starts the core with the `tun` inbounds removed from the in-memory config; the file
on disk is never modified.

### Required for node/mode switching: Clash API

Node switching, mode switching and the dashboard link use the Clash API:

```jsonc
{
  "experimental": {
    "clash_api": {
      "external_controller": "127.0.0.1:9090",
      "secret": "change-me",
      "external_ui": "ui" // optional; an http(s) URL is used as-is
    }
  }
}
```

- `external_controller` — API address. `0.0.0.0` is rewritten to `127.0.0.1` for the dashboard link.
- `secret` — API token. It is appended to the dashboard URL automatically.
- `external_ui` — optional. When set, the dashboard the core serves at `http://<controller>/ui/` is opened (an
  absolute `http(s)://` value is opened as-is). When absent, the hosted panel at
  <https://venniel.github.io/Yacd-meta> is opened.

If the config contains selectors but no `clash_api` section, the program adds one in memory with
`external_controller = "127.0.0.1:9090"` and a generated secret. This works for the current run only; the config file is
not rewritten.

### Optional: selectors for node switching

Selectors are shown in the **节点** menu. The selector `tag` is the submenu title, its `outbounds` are the items, and
`default` is the initial choice.

```jsonc
{
  "outbounds": [
    {
      "type": "selector",
      "tag": "proxy-select",
      "outbounds": ["Proxy1", "Proxy2"],
      "default": "Proxy1",
      "interrupt_exist_connections": true
    }
  ]
}
```

Switching an item sends `PUT /proxies/<tag>` to the Clash API and then closes existing connections, so the change
applies immediately.

### Optional: Clash modes

If the core reports a `mode-list`, the **模式** menu is shown. Selecting a mode sends `PATCH /configs` to the Clash API.

## Program configuration

Settings are stored in `sth.ini` next to the executable:

```ini
[sth]
; Full path to sing-box.exe. Empty means the program folder.
sb-exe =
sb-config-file = config.json
tun-start-mode = off
system-proxy-auto = 0
; Selector menu layout: "auto", "flat" or "nested"
selector-menu-layout = auto
; Optional: write a log file (relative paths are resolved against the program folder)
; log-file = sth.log
```

| Key | Description |
| --- | --- |
| `sb-exe` | Full path to the sing-box executable. A relative name is resolved against the program folder; empty means `sing-box.exe` next to `STH.exe`. |
| `sb-config-file` | sing-box config file (JSON). A relative name is resolved against the program folder first, then the core's folder. |
| `tun-start-mode` | TUN switch state: `on` or `off` (default). Updated by the app whenever TUN is toggled. |
| `system-proxy-auto` | System proxy switch state: `1` enabled, `0` disabled (default). Updated by the app whenever the proxy is toggled. |
| `selector-menu-layout` | Node menu layout: `auto` decides by item count, `flat` shows each selector as a disabled header with checkable items below, `nested` uses submenus. |
| `log-file` | Optional log file path. Relative paths are resolved against the program folder. Empty disables logging. |

Older versions stored the core folder as `sb-dir`; it is still read when `sb-exe` is absent.

The settings dialog edits `sb-exe`, `sb-config-file` and `selector-menu-layout`. Saving a different core executable or
config file restarts the program automatically.

sing-box runs with `<core folder>\config` as its working directory (created automatically). Relative paths from the
config, including `cache.db` and `external_ui`, therefore resolve inside that folder — for example put the dashboard
files in `<core folder>\config\ui` when the config uses `"external_ui": "ui"`.

Selector choices are not stored by this program. Whether they survive a restart is decided by the core: with
`experimental.cache_file.enabled`, sing-box restores the last selection itself; without it, selectors start from the
config `default`.

The TUN and system proxy switches are remembered in `sth.ini`.

### Autostart

Autostart is implemented as a scheduled task named `STH` that runs the current executable at logon with the
highest available privileges. Enabling or disabling it may prompt for UAC. If the executable is moved, re-enable
autostart so the task points at the new path.

## Command-line switches

These are mainly used internally when the program relaunches itself, but they can also be passed manually:

| Switch | Effect |
| --- | --- |
| `-tun` | Start with TUN enabled (requests elevation if needed). |
| `-restart` | Wait for an already running instance to exit before starting. |
| `-autostart-enable` | Create the autostart scheduled task. |
| `-autostart-disable` | Remove the autostart scheduled task. |

## Building and testing

Requirements:

- Go 1.26 or newer
- `golang.org/x/sys` (resolved by `go mod download`)

```sh
go build -ldflags "-H=windowsgui" -trimpath -o STH.exe .
go test .
```

`-H=windowsgui` suppresses the console window. The UI is written directly against Win32 (GDI/user32/shell32), so the
executable has no runtime dependencies beyond the standard Windows system libraries.

The Windows version resource and application icon are checked in as `sth_windows_amd64.syso`, generated from
`versioninfo.json` and `assets/sth.ico` (master `assets/sth.png`) with goversioninfo; regenerate it only when the
metadata or icon changes:

```sh
go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest -64 -icon assets/sth.ico -o sth_windows_amd64.syso versioninfo.json
```

### Source layout

| File | Purpose |
| --- | --- |
| `main.go` | Startup, single instance, DPI/COM initialization, first-run settings, elevation |
| `ui.go` | Tray icon, context menu, system proxy and TUN actions, autostart, shutdown |
| `settings.go` | Win32 settings dialog and file/folder pickers |
| `win32.go` | Win32 bindings, structs, accessibility names, small helpers |
| `drover.go` | Application state, selector/mode state, remembered TUN/proxy switches, shutdown |
| `supervisor.go` | Core process supervision (job object, stdin config, Clash API checks) |
| `apiclient.go` | Minimal Clash API client |
| `config.go` | Config parsing, requirement checks, in-memory TUN stripping, Clash API creation |
| `options.go` | INI options, including the remembered TUN and system proxy switches |
| `autostart.go`, `elevation.go`, `singleinstance.go` | Windows integration helpers |
| `systemproxy.go`, `tuncleanup.go`, `logger.go` | System proxy, Wintun cleanup, logging |
| `assets/sth.ico`, `assets/sth.png` | Executable and tray icon, the 512px PNG master, per-size `assets/sth-*.png` exports and the `assets/sth.svg` vector source |

## Limitations

- Only one config file at a time; there is no profile switching in the UI.
- Selector choices are not remembered by the program; enable `experimental.cache_file` in the sing-box config if you
  want them restored after a restart.
- TUN mode requires an elevated process and a `tun` inbound.
- The program does not modify the sing-box config file; in-memory changes (TUN removal, generated Clash API) are
  per-run only.
