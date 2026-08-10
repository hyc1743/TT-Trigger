# TT-Trigger

TT-Trigger 由一个 Chrome Manifest V3 插件和一个轻量 Windows Webhook 服务组成。外部系统调用 Webhook 后，插件会在当前活动的 `https://taoli.tools/*` 页面中填写币种或合约地址，并派发 Enter 键事件。

## 工作方式

1. Chrome 插件通过本机 `ws://127.0.0.1:8787/extension` 与服务保持连接。
2. 触发 API 只监听 `127.0.0.1:8788`，不会直接开放公网端口。
3. Tailscale Serve 在 tailnet 内提供 HTTPS 地址，并代理到本机触发 API。
4. 服务把触发内容发送给插件，并等待页面执行结果后再响应调用方。
5. 插件只操作最近获得焦点的 Chrome 窗口中的活动标签页，不会操作后台标签页或其他域名。

服务端为单文件 Go EXE，目标 Windows 机器不需要安装 Node.js、Go 或其他运行时。

## Windows 安装

### 1. 安装 Tailscale

在运行 Chrome 的 Windows 机器以及调用 Webhook 的设备上安装 Tailscale，并登录同一个 tailnet：

```text
https://tailscale.com/download/windows
```

确认 Windows 终端可以执行：

```bat
tailscale status
```

本方案使用 **Tailscale Serve**，不要启用 Tailscale Funnel。Serve 地址只对同一 tailnet 中经过认证的设备开放。

### 2. 启动服务

解压发布包后，双击 `start.bat`。首次启动会：

- 生成 `config.json`；
- 创建随机 Token；
- 在后台启动 `tt-trigger-server.exe`；
- 执行 `tailscale serve --bg http://127.0.0.1:8788`，创建 tailnet HTTPS 入口；
- 把 PID 写入 `tt-trigger.pid`，把日志写入 `logs` 目录。

复制窗口显示的 Token，并记录 `tailscale serve status` 输出的 HTTPS 地址。以后可以从 `config.json` 查看 Token。

双击 `stop.bat` 可关闭服务。修改 `config.json` 后需要先停止再启动。

### 3. 加载 Chrome 插件

1. 打开 `chrome://extensions/`。
2. 开启右上角的“开发者模式”。
3. 点击“加载已解压的扩展程序”，选择发布包中的 `extension` 文件夹。
4. 点击插件图标，直接在插件弹窗中粘贴 Token，然后点击“保存并连接”。
5. 插件弹窗显示“已连接”后即可调用 Webhook。

`TT-Trigger-Chrome-2.0.0.zip` 是插件源码压缩包；Chrome 开发者模式仍需先解压再加载。

### 4. 网络要求

- 不需要开放 Windows 防火墙端口。
- 不需要配置云服务器安全组或公网端口映射。
- `8787` 和 `8788` 都只监听 Windows 本机回环地址。
- 调用设备必须登录同一 Tailscale tailnet，并被 tailnet ACL 允许访问该设备。

## 使用 URL 直接调用

确保当前活动标签页已经打开 `https://taoli.tools/*`，并且页面上已经出现目标输入框。

在任意浏览器、Webhook 平台或程序中访问以下 URL：

```text
https://WINDOWS_HOST.YOUR_TAILNET.ts.net/trigger?token=YOUR_TOKEN&symbol=BTC
```

Token 参数放在前面，需要填写的币种或合约地址使用 `symbol` 参数。特殊字符需要进行 URL 编码。

如果填写后还需要等待 1000 毫秒并点击“Add Pair”或“添加交易对”按钮，增加可选参数：

```text
https://WINDOWS_HOST.YOUR_TAILNET.ts.net/trigger?token=YOUR_TOKEN&symbol=BTC&addPair=true
```

包含特殊字符的 symbol 示例：

```text
https://WINDOWS_HOST.YOUR_TAILNET.ts.net/trigger?token=YOUR_TOKEN&symbol=BG-P%3ASIREN%2FUSDT%2BOD-S%3ASIREN%2FUSDT&addPair=true
```

可以通过以下命令查看实际 HTTPS 地址：

```bat
tailscale serve status
```

成功响应：

```json
{"ok":true,"requestId":"70aa9bc6da3b15f92ed67cce9529a9ea"}
```

成功表示插件已完成输入赋值并派发 `input` 和 Enter `keydown` 事件，不表示 taoli.tools 的后续业务请求一定成功。

## HTTP 接口

### `GET /trigger`

- 可直接在浏览器地址栏中访问。
- URL 格式为 `/trigger?token=<token>&symbol=<币种或合约地址>`。
- 参数顺序示例中固定为 Token 在前、symbol 在后。
- `symbol` 会去除首尾空格，长度必须为 1–256 个字符。
- `addPair` 为可选布尔参数；使用 `true` 或 `1` 时，填写后等待 1000 毫秒，再点击文本为“Add Pair”或“添加交易对”的按钮。
- 同时只处理一个触发请求，不排队。

### `POST /webhook`

原有 POST 调用方式继续保留：

- 请求必须使用 `Content-Type: application/json`。
- 请求头必须包含 `Authorization: Bearer <token>`。
- 请求体为 `{"symbol":"币种或合约地址","addPair":true}`，其中 `addPair` 可省略。
- `symbol` 会去除首尾空格，长度必须为 1–256 个字符。
- 同时只处理一个触发请求，不排队。

```bash
curl -X POST 'https://WINDOWS_HOST.YOUR_TAILNET.ts.net/webhook' \
  -H 'Authorization: Bearer YOUR_TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{"symbol":"BTC"}'
```

常见错误：

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | `INVALID_SYMBOL` / `INVALID_JSON` | 请求内容无效 |
| 400 | `INVALID_ADD_PAIR` | addPair 参数不是有效布尔值 |
| 401 | `UNAUTHORIZED` | Token 缺失或错误 |
| 409 | `NO_TARGET_TAB` | 当前活动页不是 taoli.tools |
| 409 | `TRIGGER_BUSY` | 已有触发正在执行 |
| 422 | `INPUT_NOT_FOUND` | 页面中没有目标输入框 |
| 422 | `ADD_PAIR_BUTTON_NOT_FOUND` | 未找到 Add Pair 或添加交易对按钮 |
| 503 | `EXTENSION_OFFLINE` | 插件未连接 |
| 504 | `EXTENSION_TIMEOUT` | 插件未在超时前响应 |

### `GET /health`

通过 Tailscale HTTPS 地址访问时只返回 API 存活状态：

```json
{"ok":true}
```

本机访问 `http://127.0.0.1:8787/health` 时会额外返回插件连接状态。

## 配置

`config.json` 示例：

```json
{
  "extension_listen": "127.0.0.1:8787",
  "api_listen": "127.0.0.1:8788",
  "token": "由首次启动自动生成",
  "trigger_timeout_ms": 5000
}
```

- `extension_listen`：Chrome 插件的本机 WebSocket 地址，只允许回环地址。
- `api_listen`：Tailscale Serve 的本机代理目标，只允许回环地址。
- `token`：至少 32 个字符，Webhook 与插件使用同一个值。
- `trigger_timeout_ms`：等待插件结果的时间，范围 250–60000 毫秒。

旧版 `listen` 字段升级后会被忽略，服务自动使用安全的本机默认地址。

## 从源码构建

需要 Go 1.22 或更高版本。

在 Windows 上运行：

```bat
windows\build-windows.bat
```

在 Linux/macOS 上生成完整 Windows x64 发布包：

```bash
VERSION=2.0.0 ./scripts/build-release.sh
```

运行服务端测试：

```bash
go test ./...
```
