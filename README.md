# Flash Notify · 电脑闪烁提醒

把 Windows QQNT / 微信的任务栏闪烁，转成飞书 / Lark 群机器人消息。只通知“应用请求注意”，不读取聊天内容、不截图、不注入进程。

**让电脑的提醒，抵达飞书 / Lark。** 在工位直接通过飞书 / Lark 查看家里电脑的提醒，不用拿起手机、在工位登录 QQ / 微信，也不用一直挂着远程桌面。飞书 / Lark 的电脑端和手机端均可接收。

**[中文主页与安装教程](https://guajun.github.io/flash-notify/) · [下载 Release](https://github.com/guajun/flash-notify/releases/latest)**

规划中的通用“窗口闪烁 → 标准事件 → Webhook”链路见 [TODO](TODO.md)。

## 一行安装

在**运行 QQNT / 微信的 Windows 电脑**上，打开普通 PowerShell：

```powershell
irm https://guajun.github.io/flash-notify/install.ps1 | iex
```

要求：Windows 10 / 11 amd64、PowerShell 5.1+，桌面安装无需管理员权限；SSH 也可完成安装和更新，但监测需在用户桌面会话启动。安装时会隐藏输入 Webhook URL。默认开启本次监测和用户登录自启；重复运行可更新，留空保留已有 Webhook。

程序装到 `%LOCALAPPDATA%\FlashNotify\bin`，配置和日志在 `%LOCALAPPDATA%\FlashNotify`。安装器下载 Release 并检查 SHA-256；已发布二进制未进行 Authenticode 签名。

## SSH 安装

可以在 SSH 的 PowerShell 中执行同一安装命令，配置、下载、替换程序和登录自启均可完成。升级会停止同一路径的旧 daemon。SSH 安装不会在 Session 0 启动监测；完成后请在电脑已登录的桌面终端执行一次 `flash-notify start`，或在下次用户登录时自动启动。SSH 注册交互式计划任务或跨会话启动可能受 Windows 权限限制，因此安装器不依赖这些权限。

## 配置飞书 / Lark

1. 在你希望接收消息的个人群聊中添加**自定义机器人**，取得 Webhook URL。
2. 关闭机器人的**签名校验**。程序不发送签名，也不读取签名密钥。若设置了关键词或 IP 限制，请确保消息满足限制；最小配置不设置这些限制。
3. 安装时输入 URL，或运行 `flash-notify config webhook`。
4. 执行 `flash-notify test`，确认群内消息以及手机锁屏通知。

支持 `https://open.feishu.cn/open-apis/bot/v2/hook/...` 与 `https://open.larksuite.com/open-apis/bot/v2/hook/...`。不需要 lark-cli、WSL、OAuth、App ID / Secret、SSH、LAN、服务器或入站端口。

Webhook 是发送凭据，请仅保存在本机。配置文件保存明文 URL；CLI、诊断和日志不展示完整 URL。不要提交真实配置、日志或 Webhook。发送到飞书 / Lark 的信息只有设备名、应用名和时间。

通知将应用名放在第一行，方便桌面横幅预览识别：

```text
【微信】请求注意
设备：home-pc
2026-11-06 12:00:00 · 电脑闪烁提醒
```

## CLI

| 命令 | 行为 |
| --- | --- |
| `flash-notify start` | 此次开启后台 daemon；重复启动不会增加实例 |
| `flash-notify stop` | 此次关闭，不修改登录自启 |
| `flash-notify restart` | 重启并重新读取配置 |
| `flash-notify autostart on` | 开启当前用户登录自启 |
| `flash-notify autostart off` | 关闭登录自启，不停止本次运行 |
| `flash-notify config webhook` | 隐藏输入并替换唯一 Webhook；保存并通知 daemon 更新内存配置，无需重启 |
| `flash-notify config show` | 查看脱敏配置 |
| `flash-notify test` | 发一条真实测试消息 |
| `flash-notify status` | 查看 daemon、自启、最近捕获和投递情况 |
| `flash-notify doctor` | 诊断配置、会话、自启路径、daemon、捕获就绪及监测进程匹配；异常返回非零 |
| `flash-notify version` | 查看版本 |

自动化配置支持将 URL 从标准输入传给 `flash-notify config webhook --stdin`，不接受 URL 命令行参数，避免写入命令历史。单个用户会话运行一个 daemon，配置单个 Webhook。`FLASH_NOTIFY_HOME` 可覆盖数据目录用于开发测试，普通用户无需设置。

配置通过两种方式生效：`config webhook` 保存文件并通知 daemon 更新内存中的 Webhook，收到确认后返回成功；直接修改 `config.json` 后，执行 `restart` 加载全部配置。平时的闪烁事件只使用内存配置，不读取配置文件，也不监测文件变更。已在进行的投递不改目的地；daemon 繁忙时配置命令可能提示尚未确认，待投递结束后应用。首次升级到此行为需要重启一次旧 daemon。

`doctor` / `status` 会按配置的进程名列出当前桌面会话中已匹配的进程数量与 PID，并区分未匹配、只在其他会话运行和无法查询会话。QQNT 对应 `QQ.exe`，微信通常对应 `Weixin.exe` 或 `WeChat.exe`，命中其中一种即可；未启动应用是提示信息，不会单独导致诊断失败。进程匹配只能确认应用已启动，能否捕获仍需实际触发任务栏闪烁。

## 实现与限制

Go 标准库实现，`CGO_ENABLED=0`，无第三方运行时依赖。隐藏顶层窗口注册 `RegisterShellHookWindow`，接收 `HSHELL_FLASH`，通过窗口所属进程匹配 `QQ.exe`、`Weixin.exe`、`WeChat.exe`，再发 HTTPS POST。后台没有窗口扫描或截图轮询。

daemon 是**用户会话后台进程**，通过 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 的 `FlashNotify` 值在登录时启动。它不是 Session 0 Windows Service：系统服务无法直接接收另一个桌面会话的窗口事件。“开机自启”实际指开机后用户登录自启；未登录、注销或休眠时不监测。

- 默认每个应用连续闪烁间隔小于 10 秒时合并为一轮，不是逐条消息计数。持续闪烁不会持续刷屏。
- 静音、前台聊天、关闭任务栏闪烁、仅托盘图标变化等情况可能不触发；其他需要注意的提示也可能触发。
- Microsoft 提示 Shell Hook 接口未来可能变化，因此无法保证所有 Windows 或应用版本。
- 临时网络错误、HTTP 429 / 5xx 和已知限流码最多尝试 3 次（等待 2 秒、4 秒）。重试后失败只记日志；无离线持久队列。请求结果不明时重试可能重复。
- HTTP 与业务状态码都成功才记录“Webhook 已接受”；这不代表手机已展示或用户已读。
- 内存队列上限 128 条；拥塞时丢弃新增事件并记录日志。
- 手机需要开启通知、取消通知群免打扰；检查“桌面在线时关闭手机通知”和“接收其他账号通知”设置。锁屏与多账号通知需要实际验证。

查看 `%LOCALAPPDATA%\FlashNotify\daemon.log` 和 `state.json` 可诊断。`state.json` 在停止后仍保留最近运行记录，以 `status` 的当前 daemon 状态为准。日志暂不自动轮转，可停止后按需清理。

## 手动安装 / 卸载

手动下载 Release zip，校验 `checksums.txt`，解压到固定位置：

```powershell
.\flash-notify.exe config webhook
.\flash-notify.exe autostart on
.\flash-notify.exe start
.\flash-notify.exe test
```

卸载前运行 `flash-notify autostart off`、`flash-notify stop`，删除 `%LOCALAPPDATA%\FlashNotify`，并从当前用户 PATH 中移除 `%LOCALAPPDATA%\FlashNotify\bin`。

## 开发

Go 1.24+，Windows amd64：

```powershell
go test -v ./...
go vet ./...
go build -trimpath -o dist/flash-notify.exe .
.\scripts\release.ps1 -Version v0.1.0
```

测试覆盖 Webhook 业务错误、无签名请求、重定向限制、URL 脱敏、配置替换、合并逻辑，以及真实 Windows 桌面 Shell FLASH → 本地 HTTP 接收。没有交互式桌面的 CI 会跳过真实窗口集成测试。

## 参考

- [Microsoft RegisterShellHookWindow](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-registershellhookwindow)
- [飞书自定义机器人](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot)
- [Lark 自定义机器人](https://open.larksuite.com/document/client-docs/bot-v3/add-custom-bot)

MIT License。项目与腾讯、飞书、Lark 无隶属关系。
