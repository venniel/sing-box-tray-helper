# sing-box 托盘助手

本项目基于 [sing-box-drover](https://github.com/hdrover/sing-box-drover) 二次修改（fork），在此感谢原作者的工作。

一个轻量级 Windows 托盘控制器：在后台运行官方 `sing-box` 核心，只提供一小块原生控制面——系统代理、TUN 模式、Clash 模式与节点切换、面板入口。目标是保留普通 sing-box 配置的全部能力，只补上 Windows 侧的胶水，而不是做成又一个一体化 GUI 客户端。

- 直接使用官方 `sing-box.exe`，不内嵌、不打包
- 一份 JSON 配置，原样使用
- 原生 Win32 控件（不依赖 GUI 工具包），托盘菜单与设置对话框带有明确的无障碍名称，NVDA、争渡读屏等读屏软件可直接导航
- 空闲开销小：启动后不做周期性 HTTP 轮询，只在启动、打开菜单和切换时访问 Clash API

## 功能

- 托盘图标反映当前状态：TUN 已启用、系统代理已启用或空闲
- 左键单击托盘图标打开设置对话框；右键打开菜单：
  - **更多**：重启、开机自启、项目主页
  - **模式**：Clash 模式切换，列表来自 Clash API
  - **节点**：selector 出站。核心当前实际使用的出站会被标记，即使是在面板里从外部切换的；程序启动时不会向核心推送选择，持久化交给核心
  - **TUN 模式**：配置中有 `tun` 入站时可用，需要时请求提权
  - **系统代理**：切换 Windows 代理设置
  - **面板**：配置了 `external_ui` 时打开核心提供的面板，否则打开在线 yacd
  - **退出**：优雅停止核心并退出
- 若上次退出前系统代理仍开启，下次启动时恢复；正常退出时关闭
- TUN 状态跨重启记忆；开启 TUN 会以管理员权限重新启动（UAC）
- 每个用户会话只允许一个实例，重复启动会被拒绝
- 可选：通过计划任务实现开机自启

## 环境要求

- Windows 10 或更高版本（64 位）
- 官方 `sing-box.exe`（Windows 版），上游下载地址：<https://github.com/SagerNet/sing-box/releases>
- TUN 模式需要官方 Windows 包内的 `wintun.dll`，与 `sing-box.exe` 放在一起
- 一份满足下列要求的 sing-box 配置

## 快速开始

1. 获取 `STH.exe`：从 [latest release](../../releases/latest) 下载，或自行编译：

   ```sh
   go build -ldflags "-H=windowsgui -s -w" -trimpath -o STH.exe .
   ```

2. 将 `STH.exe` 放在任意目录。首次保存设置时会生成 `sth.ini`。
3. 将 `sing-box.exe` 放在 `STH.exe` 同级，或用 `sb-exe` 指向其完整路径。
4. 运行 `STH.exe`。首次启动会打开设置对话框，选择核心和配置文件；保存后出现托盘图标并启动核心。

托盘菜单与设置对话框为中文。

## sing-box 配置要求

### 必需：mixed 入站

程序依赖 `mixed` 入站：读取其中的 `listen` 与 `listen_port` 来配置 Windows 系统代理，缺少时拒绝启动。

```jsonc
{
  "inbounds": [
    {
      "type": "mixed",
      "tag": "mixed-in", // 名称任意，程序不使用
      "listen": "127.0.0.1",
      "listen_port": 1080
    }
  ]
}
```

### 可选：tun 入站

配置中存在 `type` 为 `tun` 的入站时，菜单里才会出现 TUN 项。

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

关闭 TUN 时，程序先从内存中的配置里移除 `tun` 入站再启动核心；磁盘上的配置文件始终不被修改。

### 节点/模式切换与面板：Clash API

节点切换、模式切换和面板链接都依赖 Clash API：

```jsonc
{
  "experimental": {
    "clash_api": {
      "external_controller": "127.0.0.1:9090",
      "secret": "change-me",
      "external_ui": "ui" // 可选；也可直接写 http(s):// 地址
    }
  }
}
```

- `external_controller`：API 地址。生成面板链接时，`0.0.0.0` 会被改写为 `127.0.0.1`。
- `secret`：API 令牌，会自动附加到面板链接。
- `external_ui`：可选。设置后打开核心在 `http://<controller>/ui/` 提供的面板（绝对 `http(s)://` 地址按原样打开）；未设置时打开在线面板 <https://venniel.github.io/Yacd-meta>。

配置中有 selector 但没有 `clash_api` 时，程序会在内存中补一个：`external_controller = "127.0.0.1:9090"` 并生成随机 `secret`。仅对当次运行有效，不会写回配置文件。

### 可选：用于节点切换的 selector

selector 出现在 **节点** 菜单中：`tag` 是子菜单标题，`outbounds` 是菜单项，`default` 是初始选项。

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

切换时程序向 Clash API 发送 `PUT /proxies/<tag>`，随后关闭已有连接，使改动立即生效。

### 可选：Clash 模式

核心上报 `mode-list` 时显示 **模式** 菜单，选择后向 Clash API 发送 `PATCH /configs`。

## 程序配置

设置保存在可执行文件同目录的 `sth.ini`：

```ini
[sth]
; sing-box.exe 的完整路径。留空表示程序所在目录。
sb-exe =
sb-config-file = config.json
tun-start-mode = off
system-proxy-auto = 0
; 节点菜单布局：auto、flat 或 nested
selector-menu-layout = auto
; 可选：写日志文件（相对路径相对于程序目录）
; log-file = sth.log
```

| 键 | 说明 |
| --- | --- |
| `sb-exe` | `sing-box` 可执行文件的完整路径。相对名称按程序目录解析；留空表示与 `STH.exe` 同级的 `sing-box.exe`。 |
| `sb-config-file` | sing-box 配置文件（JSON）。相对名称先按程序目录解析，再按核心目录解析。 |
| `tun-start-mode` | TUN 开关状态：`on` 或 `off`（默认）。切换 TUN 时由程序更新。 |
| `system-proxy-auto` | 系统代理开关状态：`1` 启用，`0` 禁用（默认）。切换代理时由程序更新。 |
| `selector-menu-layout` | 节点菜单布局：`auto` 按条目数量决定，`flat` 每个 selector 作为不可点击的标题并列出可勾选项，`nested` 使用子菜单。 |
| `log-file` | 可选的日志文件路径，相对路径按程序目录解析；留空则不写日志。 |

旧版本用 `sb-dir` 记录核心目录，`sb-exe` 不存在时仍会读取它。

设置对话框可修改 `sb-exe`、`sb-config-file` 和 `selector-menu-layout`；保存后若核心或配置文件发生变化，程序会自动重启。

核心的工作目录为 `<核心目录>\config`（自动创建），配置中的相对路径（包括 `cache.db` 和 `external_ui`）都相对于它解析——例如配置写 `"external_ui": "ui"` 时，面板文件应放在 `<核心目录>\config\ui`。

节点选择不由本程序保存，能否跨重启保留取决于核心：启用 `experimental.cache_file.enabled` 时由 sing-box 自行恢复，否则从配置中的 `default` 开始。

TUN 与系统代理的开关状态记录在 `sth.ini` 中。

### 开机自启

自启由名为 `STH` 的计划任务实现：登录时以最高可用权限运行当前可执行文件，启用或关闭时可能弹出 UAC。移动可执行文件后需要重新启用自启，以更新任务中的路径。

## 命令行开关

主要用于程序自我重启，也可手动传入：

| 开关 | 作用 |
| --- | --- |
| `-tun` | 以启用 TUN 的方式启动（必要时请求提权）。 |
| `-restart` | 等待已运行的实例退出后再启动。 |
| `-autostart-enable` | 创建自启计划任务。 |
| `-autostart-disable` | 删除自启计划任务。 |

## 构建与测试

要求：

- Go 1.26 或更高版本
- `golang.org/x/sys`（由 `go mod download` 解析）

```sh
go build -ldflags "-H=windowsgui -s -w" -trimpath -o STH.exe .
go test .
```

`-H=windowsgui` 用于隐藏控制台窗口，`-s -w` 去掉符号表和调试信息以减小体积。界面直接基于 Win32（GDI/user32/shell32）实现，运行时除 Windows 系统库外没有其他依赖。

Windows 版本资源与应用图标以 `sth_windows_amd64.syso` 的形式入库，由 `versioninfo.json` 和 `assets/sth.ico`（母版为 `assets/sth.png`）经 goversioninfo 生成。仅在元数据或图标变化时需要重新生成：

```sh
go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest -64 -icon assets/sth.ico -o sth_windows_amd64.syso versioninfo.json
```

每次推送到仓库都会由 GitHub Actions 交叉编译 Windows x64 版本，并更新唯一的 `latest` release；也可以直接从 [latest release](../../releases/latest) 下载。

## 源码结构

| 文件 | 作用 |
| --- | --- |
| `main.go` | 启动、单实例、DPI/COM 初始化、首次运行设置、提权 |
| `ui.go` | 托盘图标、右键菜单、系统代理与 TUN 操作、自启、退出 |
| `settings.go` | Win32 设置对话框与文件/目录选择器 |
| `win32.go` | Win32 绑定、结构体、无障碍名称与辅助函数 |
| `drover.go` | 应用状态、节点/模式状态、TUN 与代理开关记忆、退出 |
| `supervisor.go` | 核心进程管理（Job 对象、stdin 配置、Clash API 检查） |
| `apiclient.go` | 精简的 Clash API 客户端 |
| `config.go` | 配置解析、要求检查、内存中移除 TUN、生成 Clash API |
| `options.go` | INI 选项，含 TUN 与系统代理的开关记忆 |
| `autostart.go`、`elevation.go`、`singleinstance.go` | Windows 集成辅助 |
| `systemproxy.go`、`tuncleanup.go`、`logger.go` | 系统代理、Wintun 清理、日志 |
| `assets/sth.ico`、`assets/sth.png` | 可执行文件与托盘图标；`assets/sth.png` 为 512px 母版，`assets/sth-*.png` 为各尺寸导出，`assets/sth.svg` 为矢量源文件 |

## 已知限制

- 一次只能使用一份配置文件，界面没有配置切换功能。
- 程序不记忆节点选择；需要重启后保留时，请在 sing-box 配置中启用 `experimental.cache_file`。
- TUN 模式需要提权，且配置中必须有 `tun` 入站。
- 程序不修改 sing-box 配置文件；内存中的改动（移除 TUN、生成 Clash API）仅在当次运行有效。
