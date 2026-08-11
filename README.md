# TT-Trigger 4.0

TT-Trigger 可以从外部程序触发 Chrome，在当前 `https://taoli.tools/*` 页面填写交易对，并可等待 1000ms 后点击 **Add Pair / 添加交易对**。

支持两种互斥的连接方式：

- **本地 / Tailscale**：Windows 本地服务接收带时间戳、nonce 和 HMAC-SHA256 的 `POST /webhook`。
- **Cloudflare 云端 E2EE**：Chrome 插件直接连接 Cloudflare Worker；请求与执行结果只在调用方和插件中解密，不需要在电脑上运行本地服务。

示例 `symbol`：

```text
BG-P:SIREN/USDT+OD-S:SIREN/USDT
```

## 安装 Chrome 插件

1. 下载并解压 `dist/TT-Trigger-4.0.0-windows-x64.zip`。
2. 打开 `chrome://extensions`，启用“开发者模式”。
3. 点击“加载已解压的扩展程序”，选择压缩包内的 `extension` 目录。
4. 点击 TT Trigger 图标，在弹窗选择本地或云端模式。

最低支持 Chrome 116。插件配置和E2EE密钥只存放在 `chrome.storage.local`，不会使用Chrome同步。

## 方式一：localhost / Tailscale

### 启动

1. 运行 `start.bat`。首次运行会生成唯一的 `config.json`。
2. 打开插件，选择“本地 / Tailscale”。
3. 将 `config.json` 中的 `extension_token` 填入插件，点击“保存并连接”。
4. 插件显示“已连接”后即可调用。

服务始终监听：

```text
http://127.0.0.1:8788/webhook
```

如果Windows已安装并连接Tailscale，脚本还会自动监听检测到的 `100.64.0.0/10` IPv4：

```text
http://TAILSCALE_IP:8788/webhook
```

服务不会监听普通局域网IP或 `0.0.0.0`。未安装或未启动Tailscale不会阻止localhost模式运行。

### Python调用

`invoke-trigger.py` 默认复用同目录的 `config.json`：

```powershell
python invoke-trigger.py --symbol BTC
python invoke-trigger.py --symbol "BG-P:SIREN/USDT+OD-S:SIREN/USDT" --add-pair
```

调用Tailscale地址时覆盖Base URL：

```powershell
python invoke-trigger.py --base-url http://100.100.20.30:8788 --symbol BTC
```

本地请求格式：

```http
POST /webhook
Content-Type: application/json
X-TT-Key-Id: default
X-TT-Timestamp: 1786400000
X-TT-Nonce: RANDOM_BASE64URL
X-TT-Signature: LOWERCASE_HEX_HMAC_SHA256

{"requestId":"UUID","symbol":"BTC","addPair":false}
```

签名原文：

```text
TT-TRIGGER-V1
POST
/webhook
{timestamp}
{nonce}
{lowercase SHA256 of exact body}
```

允许时间误差默认30秒；nonce只能使用一次；相同requestId和相同参数会返回缓存结果。

### 管理

| 文件 | 用途 |
|---|---|
| `start.bat` | 启动localhost和可用的Tailscale监听 |
| `stop.bat` | 停止本地服务 |
| `status.bat` | 查看状态 |
| `configure.bat` | 显示本地/云端配置说明 |
| `manage-keys.bat` | 新增、列出或吊销本地HMAC密钥 |

## 方式二：Cloudflare E2EE

云端模式只要求Chrome插件保持运行，不需要 `start.bat`。中继只转发固定长度密文。

### 连接中继

1. 在插件选择“云端 E2EE”。
2. 填写开发者提供的中继URL，或填写自部署的 `https://*.workers.dev` 地址。
3. 开发者共享中继需要一次性激活码；自部署首次设备模式可留空。
4. 点击“注册并连接”，按Chrome提示授予该中继Origin的访问权限。
5. 在“新增调用方”输入名称，点击“新增并导出”，保存下载的调用方JSON。

每个调用方拥有独立中继Token和独立E2EE密钥。插件支持导出、轮换和吊销；吊销会同时删除插件端解密密钥并撤销Cloudflare中继权限。

调用方文件示例：

```json
{
  "version": 1,
  "mode": "cloud_e2ee",
  "relay_url": "https://relay.example.workers.dev",
  "device_id": "...",
  "key_id": "...",
  "relay_token": "...",
  "relay_grant": "...",
  "secret": "..."
}
```

它不包含插件Token、设备管理凭据或其他调用方密钥。

### Python调用云端

先安装AES-GCM依赖：

```powershell
install-cloud-client.bat
```

然后直接使用插件导出的文件：

```powershell
python invoke-trigger.py --config C:\path\client.json --symbol BTC
python invoke-trigger.py --config C:\path\client.json --symbol "BG-P:SIREN/USDT+OD-S:SIREN/USDT" --add-pair
```

只有云端模式依赖 `cryptography`；本地模式仍只使用Python标准库。

### 自部署Cloudflare Worker

源码位于 [`cloudflare/`](cloudflare/README.md)，使用Workers Free和Durable Objects。完整的截图无关、可逐条复制执行的部署说明请阅读 **[Cloudflare Relay部署指南](cloudflare/README.md)**；Windows发布包中也已包含整个 `cloudflare` 目录。

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/hyc1743/TT-Trigger/tree/main/cloudflare)

通过Cloudflare控制台连接整个GitHub仓库时，构建设置必须使用：

```text
Root directory: cloudflare
Build command: npm run typecheck
Deploy command: npx wrangler deploy
Build output directory: 留空
```

如果Root directory仍是仓库根目录，Wrangler会误判成静态站点并报告 `Could not detect a directory containing static files`。

Windows快速部署步骤：

```powershell
cd C:\你解压的位置\cloudflare
npm install
powershell -ExecutionPolicy Bypass -File .\deploy.ps1
```

部署期间浏览器会要求登录Cloudflare。成功后保存PowerShell输出的：

1. `https://*.workers.dev` Worker URL；
2. 管理员Token。

先检查服务：

```powershell
Invoke-RestMethod https://你的Worker地址/health
```

然后在插件中选择“云端 E2EE”，填写Worker根地址，首次自部署的激活码留空，点击“注册并连接”。默认 `first_device` 模式会在首个设备注册后关闭无激活码注册。

需要注册更多Chrome设备时，将 `wrangler.toml` 中的 `ENROLLMENT_MODE` 改为 `activation_required`，重新运行 `npx wrangler deploy`，再生成一次性激活码：

```powershell
$env:TT_RELAY_URL='https://relay.example.workers.dev'
$env:TT_ADMIN_TOKEN='部署时生成的管理员Token'
npm run admin -- codes 10 86400
```

自用实例可以显式设置 `OPEN_ENROLLMENT=true` 开放注册。

## 云端协议与隐私

- 每个调用方的32字节Secret通过HKDF-SHA256派生请求/响应各自的AES-256-GCM和HMAC-SHA256密钥。
- `symbol`、`addPair`和执行结果先编码为“2字节长度 + JSON + 随机填充”的固定2048字节明文，再加密。
- 请求使用 `POST JSON + timestamp + nonce + HMAC请求头`；插件在解密和操作页面前验证时间窗、HMAC、nonce和requestId。
- 插件正常回应时外层HTTP统一为200，真实 `{ok, code, message}` 位于加密响应内。
- 插件离线返回503、设备忙返回429、中继等待超时返回504；密文不会离线排队或持久化。
- 中继不记录正文、密文、Token、Grant或完整IP。

Cloudflare仍可观察设备/调用方随机标识、来源IP、在线状态、请求时间和固定长度流量，但无法取得业务明文或网页执行结果。

## 配置迁移

- 3.x的本地HMAC密钥、插件Token和localhost/Tailscale行为保持兼容。
- 旧 `public_caddy` 配置自动备份为 `config.json.pre-4.0.bak` 并迁移到 `local_tailscale`。
- TT-Trigger 4.0不再下载、启动、停止或修改Caddy；已安装的Caddy由用户自行保留或删除。

## 构建与测试

要求Go 1.22+、Node.js、npm和Python 3：

```bash
npm run check:extension
npm run test:extension
python3 -m unittest discover -s tests -p '*_test.py'
go test ./...
cd cloudflare && npm ci && npm test && npm run typecheck
VERSION=4.0.0 ./scripts/build-release.sh
```

发布产物：

```text
dist/TT-Trigger-4.0.0-windows-x64.zip
dist/TT-Trigger-4.0.0-windows-x64.zip.sha256
dist/TT-Trigger-4.0.0-cloudflare.zip
dist/TT-Trigger-4.0.0-cloudflare.zip.sha256
```
