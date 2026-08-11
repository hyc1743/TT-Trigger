# TT-Trigger

TT-Trigger 是一个 Windows x64 本地服务和 Chrome Manifest V3 插件。外部程序向服务发送经过 HMAC-SHA256 签名的 `POST /webhook` 请求后，插件会在当前 `https://taoli.tools/*` 页面填写交易对，并可选择等待 1000ms 后点击 **Add Pair / 添加交易对**。

当前版本：**3.0.0**。从此版本开始，旧的 `GET /trigger?token=...` 和 Bearer Token 接口已移除。

## 工作方式

```text
调用方 -- HMAC POST --> TT-Trigger -- 本机 WebSocket --> Chrome 插件 --> taoli.tools
```

- 外部 API 使用独立的 HMAC 密钥，可为不同调用方创建不同 `keyId`。
- Chrome 插件只连接 `ws://127.0.0.1:8787/extension`，使用独立的 `extension_token`。
- 触发 API 始终保留 `http://127.0.0.1:8788`，不会把插件 WebSocket 暴露到外部网络。

## Windows 快速开始

### 1. 下载并解压

下载 `dist/TT-Trigger-3.0.0-windows-x64.zip`，完整解压到一个可长期保留的目录。运行时不需要安装 Go、Node.js 或 Caddy。

### 2. 启动并选择方案

双击 `start.bat`。首次启动会生成密钥并要求选择：

1. **localhost + 可选 Tailscale（HTTP）**
2. **公网域名 + Caddy + Let's Encrypt（HTTPS）**

选择保存在 `deployment.json`，以后双击 `start.bat` 会直接按原方案启动。要更换方案，运行 `configure.bat`。

首次运行会在窗口中显示：

- `Extension token`：填入 Chrome 插件。
- `Default HMAC key ID` 和 `Default HMAC secret`：供外部调用方签名。

这些密钥也保存在 `config.json`。不要上传、截图或发送该文件。

### 3. 加载 Chrome 插件

1. 打开 `chrome://extensions/`。
2. 开启右上角“开发者模式”。
3. 点击“加载已解压的扩展程序”，选择发布目录中的 `extension` 文件夹。
4. 点击工具栏的 **TT Trigger**。
5. 把 `config.json` 中的 `extension_token` 填入“插件连接 Token”，点击“保存并连接”。
6. 保持需要操作的 `https://taoli.tools/*` 页面为当前活动标签页。

插件 Token 与 HMAC secret 不是同一个值，不能互换。

## 运行方案

### 方案一：localhost + 可选 Tailscale

服务始终监听：

```text
http://127.0.0.1:8788/webhook
```

启动脚本会自动查找 Tailscale：

- Tailscale 已安装、已登录且在线：额外监听自动获取的 `100.x.x.x:8788`。
- 未安装、未启动或未登录：显示警告，但 localhost 仍然正常运行。
- Tailscale IP 或状态变化后，重启 TT-Trigger 重新探测。

该方案的 API 是 HTTP。Tailscale 负责设备之间链路加密，HMAC 负责请求身份、完整性和防重放。不要把 `8788` 直接转发到普通公网。

### 方案二：公网 HTTPS + Caddy

配置时输入一个域名，例如 `trigger.example.com`。脚本将：

1. 从 Caddy 官方 GitHub Release 下载固定版本 Caddy `v2.11.4`。
2. 校验下载 ZIP 的固定 SHA-256。
3. 让 Go 服务只监听 localhost。
4. 启动 Caddy，把公网 `POST /webhook` 反代到 `127.0.0.1:8788`。
5. 使用 Let's Encrypt 自动申请证书，并在Caddy运行期间自动续签。

公网模式的前置条件：

- 域名 A/AAAA 记录正确指向本机公网地址。
- 公网 TCP 80 和 443 能到达这台电脑。
- Windows 防火墙和安全软件允许 Caddy 接收 80/443。
- 使用路由器时已配置端口转发。
- 网络不能受无法配置端口映射的 CGNAT 阻断。
- 80/443 没有被 IIS、Nginx 或其他程序占用。

脚本只检测并提示防火墙问题，不会自动修改防火墙规则。证书、Caddy账户和续签数据保存在 `runtime/`，不要在证书有效期内随意删除。

Caddy不是Windows服务：`start.bat` 同时启动 TT-Trigger 和 Caddy，`stop.bat` 同时停止它们。电脑重启或用户注销后需要再次启动；程序停止期间无法接收请求或执行续签。证书申请或HTTPS检查失败时，两者都会停止，详细原因位于 `logs/`。

## 调用示例

发布包包含 `invoke-trigger.ps1`，它会自动生成 UUID、timestamp、nonce 和 HMAC 签名。

推荐用环境变量提供凭据，减少密钥出现在命令历史中的机会：

```powershell
$env:TT_KEY_ID = 'default'
$env:TT_HMAC_SECRET = 'config.json 中对应的 secret'

# localhost / Tailscale 模式
.\invoke-trigger.ps1 `
  -BaseUrl 'http://127.0.0.1:8788' `
  -Symbol 'BG-P:SIREN/USDT+OD-S:SIREN/USDT' `
  -AddPair

# 公网模式
.\invoke-trigger.ps1 `
  -BaseUrl 'https://trigger.example.com' `
  -Symbol 'BTC'
```

`-AddPair` 未提供时只填写 symbol 并触发 Enter；提供时再等待1000ms点击 “Add Pair” 或“添加交易对”。

## HMAC API 规范

### 请求

```http
POST /webhook HTTP/1.1
Content-Type: application/json
X-TT-Key-Id: default
X-TT-Timestamp: 1786400000
X-TT-Nonce: 8Yx3tQ7Mh7u9J0G2C7jzGw
X-TT-Signature: 64位小写十六进制字符串

{"requestId":"550e8400-e29b-41d4-a716-446655440000","symbol":"BTC","addPair":true}
```

- `requestId`：必填 UUID，用于防止超时重试造成重复操作。
- `symbol`：必填，去除首尾空白后为1–256个Unicode字符。
- `addPair`：可选布尔值，默认 `false`。
- 未声明的JSON属性会被拒绝。

### 签名算法

1. 将 HMAC secret 按无填充 Base64URL 解码为32字节密钥。
2. 对**实际发送的原始JSON字节**计算 SHA-256，输出小写十六进制。
3. 用换行符连接以下内容，最后没有换行：

   ```text
   TT-TRIGGER-V1
   POST
   /webhook
   {X-TT-Timestamp}
   {X-TT-Nonce}
   {SHA256(raw JSON body)}
   ```

4. 使用密钥对上述 UTF-8/ASCII 字符串计算 HMAC-SHA256，把小写十六进制结果放入 `X-TT-Signature`。

`X-TT-Timestamp` 是 Unix 秒。默认只接受与服务端时间相差 ±30 秒的请求，请确保双方开启系统时间同步。`X-TT-Nonce` 必须是无填充 Base64URL，解码后16–64字节；同一 keyId 下的nonce只能使用一次。nonce记录和幂等缓存都只存在于内存，服务重启后会清空。

相同 `keyId + requestId` 在10分钟内不会重复执行；如果请求参数不同，返回 `409 REQUEST_ID_CONFLICT`。

### 常见响应

```json
{"ok":true,"requestId":"550e8400-e29b-41d4-a716-446655440000"}
```

| HTTP | code | 含义 |
|---|---|---|
| 401 | `INVALID_SIGNATURE` | 缺少或错误的签名字段 |
| 401 | `STALE_REQUEST` | timestamp超出允许范围 |
| 401 | `REPLAYED_REQUEST` | nonce已使用 |
| 409 | `REQUEST_ID_CONFLICT` | requestId被用于不同参数 |
| 409 | `TRIGGER_BUSY` | 另一个不同触发正在执行 |
| 503 | `EXTENSION_OFFLINE` | Chrome插件未连接 |
| 504 | `EXTENSION_TIMEOUT` | 插件未在超时时间内返回 |
| 422 | `INPUT_NOT_FOUND` | 页面中没有找到目标输入框 |
| 422 | `ADD_PAIR_BUTTON_NOT_FOUND` | 未找到添加交易对按钮 |

`GET /health` 只用于 localhost/Tailscale 本地检查；Caddy不会将其暴露到公网。

## 密钥管理与轮换

运行 `manage-keys.bat` 可以列出、新增或吊销 HMAC key。不同调用方应使用不同keyId。

无中断轮换流程：

1. 新增一个keyId并安全保存窗口中只显示一次的新secret。
2. 修改调用方配置并验证新密钥。
3. 再运行 `manage-keys.bat` 吊销旧keyId。

密钥变更时脚本会联合重启正在运行的 TT-Trigger/Caddy。最后一把HMAC密钥不能删除。

## 管理脚本

| 文件 | 用途 |
|---|---|
| `start.bat` | 按已保存模式联合启动 |
| `stop.bat` | 联合停止服务和Caddy |
| `status.bat` | 显示模式和进程状态 |
| `configure.bat` | 停止当前进程并重新选择方案 |
| `manage-keys.bat` | 创建、列出和吊销HMAC密钥 |
| `invoke-trigger.ps1` | 生成签名并调用webhook |

日志位于 `logs/`。`trigger_timeout_ms` 表示服务把触发消息发送给插件后，等待页面操作结果的最长毫秒数，允许范围250–60000。

## 配置

```json
{
  "extension_listen": "127.0.0.1:8787",
  "api_listen": "127.0.0.1:8788",
  "extension_token": "自动生成",
  "hmac_keys": [
    {
      "id": "default",
      "secret": "32字节随机密钥的无填充Base64URL"
    }
  ],
  "signature_max_skew_seconds": 30,
  "trigger_timeout_ms": 5000
}
```

`api_listen` 和 `extension_listen` 只允许回环地址。Tailscale地址由启动脚本作为临时参数传入，不能通过配置文件改成普通局域网或公网地址。

升级旧版本时，原 `token` 会保留为 `extension_token`，同时生成新的HMAC密钥；旧文件备份为 `config.json.pre-3.0.bak`。

## 从源码构建

需要 Go 1.22+、Node.js 和 Python 3：

```bash
go test ./...
npm run test:extension
npm run check:extension
VERSION=3.0.0 ./scripts/build-release.sh
```

输出：

```text
dist/TT-Trigger-3.0.0-windows-x64.zip
dist/TT-Trigger-3.0.0-windows-x64.zip.sha256
```
