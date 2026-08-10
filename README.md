# TT-Trigger

TT-Trigger 由一个 Chrome Manifest V3 插件和一个轻量 Windows Webhook 服务组成。外部系统调用 Webhook 后，插件会在当前活动的 `https://taoli.tools/*` 页面中填写币种或合约地址，并派发 Enter 键事件。

## 工作方式

1. Windows 服务监听 `0.0.0.0:8787`，接收 URL 中携带 Token 和 symbol 的触发请求。
2. Chrome 插件通过本机 `ws://127.0.0.1:8787/extension` 与服务保持连接。
3. 服务把触发内容发送给插件，并等待页面执行结果后再响应调用方。
4. 插件只操作最近获得焦点的 Chrome 窗口中的活动标签页，不会操作后台标签页或其他域名。

服务端为单文件 Go EXE，目标 Windows 机器不需要安装 Node.js、Go 或其他运行时。

## Windows 安装

### 1. 启动服务

解压发布包后，双击 `start.bat`。首次启动会：

- 生成 `config.json`；
- 创建随机 Token；
- 在后台启动 `tt-trigger-server.exe`；
- 把 PID 写入 `tt-trigger.pid`，把日志写入 `logs` 目录。

复制窗口显示的 Token。以后可以从 `config.json` 查看它。

双击 `stop.bat` 可关闭服务。修改 `config.json` 后需要先停止再启动。

### 2. 加载 Chrome 插件

1. 打开 `chrome://extensions/`。
2. 开启右上角的“开发者模式”。
3. 点击“加载已解压的扩展程序”，选择发布包中的 `extension` 文件夹。
4. 点击插件图标，直接在插件弹窗中粘贴 Token，然后点击“保存并连接”。
5. 插件弹窗显示“已连接”后即可调用 Webhook。

`TT-Trigger-Chrome-1.1.0.zip` 是插件源码压缩包；Chrome 开发者模式仍需先解压再加载。

### 3. 开放公网端口

以管理员身份打开命令提示符，为默认端口添加 Windows 防火墙规则：

```bat
netsh advfirewall firewall add rule name="TT-Trigger Webhook" dir=in action=allow protocol=TCP localport=8787
```

云服务器安全组也需要允许 TCP 8787。插件默认连接本机 8787 端口。

## 使用 URL 直接调用

确保当前活动标签页已经打开 `https://taoli.tools/*`，并且页面上已经出现目标输入框。

在任意浏览器、Webhook 平台或程序中访问以下 URL：

```text
http://YOUR_PUBLIC_IP:8787/trigger?token=YOUR_TOKEN&symbol=BTC
```

Token 参数放在前面，需要填写的币种或合约地址使用 `symbol` 参数。特殊字符需要进行 URL 编码。

例如调用本机服务：

```text
http://127.0.0.1:8787/trigger?token=YOUR_TOKEN&symbol=BTC
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
- 同时只处理一个触发请求，不排队。

### `POST /webhook`

原有 POST 调用方式继续保留：

- 请求必须使用 `Content-Type: application/json`。
- 请求头必须包含 `Authorization: Bearer <token>`。
- 请求体为 `{"symbol":"币种或合约地址"}`。
- `symbol` 会去除首尾空格，长度必须为 1–256 个字符。
- 同时只处理一个触发请求，不排队。

```bash
curl -X POST 'http://YOUR_PUBLIC_IP:8787/webhook' \
  -H 'Authorization: Bearer YOUR_TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{"symbol":"BTC"}'
```

常见错误：

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | `INVALID_SYMBOL` / `INVALID_JSON` | 请求内容无效 |
| 401 | `UNAUTHORIZED` | Token 缺失或错误 |
| 409 | `NO_TARGET_TAB` | 当前活动页不是 taoli.tools |
| 409 | `TRIGGER_BUSY` | 已有触发正在执行 |
| 422 | `INPUT_NOT_FOUND` | 页面中没有目标输入框 |
| 503 | `EXTENSION_OFFLINE` | 插件未连接 |
| 504 | `EXTENSION_TIMEOUT` | 插件未在超时前响应 |

### `GET /health`

无需鉴权，用于检查服务和插件连接状态：

```json
{"extensionConnected":true,"ok":true}
```

## 配置

`config.json` 示例：

```json
{
  "listen": "0.0.0.0:8787",
  "token": "由首次启动自动生成",
  "trigger_timeout_ms": 5000
}
```

- `listen`：HTTP/WebSocket 监听地址。
- `token`：至少 32 个字符，Webhook 与插件使用同一个值。
- `trigger_timeout_ms`：等待插件结果的时间，范围 250–60000 毫秒。

## 从源码构建

需要 Go 1.22 或更高版本。

在 Windows 上运行：

```bat
windows\build-windows.bat
```

在 Linux/macOS 上生成完整 Windows x64 发布包：

```bash
VERSION=1.1.0 ./scripts/build-release.sh
```

运行服务端测试：

```bash
go test ./...
```
