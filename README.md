# TT-Trigger 4.0

TT-Trigger 可以从外部程序触发 Chrome，在当前 `https://taoli.tools/*` 页面填写交易对，并可等待 1000ms 后点击 **Add Pair / 添加交易对**。

支持两种互斥的连接方式：

- **本地 / Tailscale**：Windows 本地服务接收带时间戳、nonce 和 HMAC-SHA256 的 `POST /webhook`。
- **Cloudflare 云端 E2EE**：Chrome 插件直接连接 Cloudflare Worker；请求与执行结果只在调用方和插件中解密，不需要在电脑上运行本地服务。

示例 `symbol`：

```text
BG-P:SIREN/USDT+OD-S:SIREN/USDT
```

## 发布文件与下载选择

每次发布只提供以下4个主要文件，互相独立：

| 文件 | 内容 |
|---|---|
| `invoke-trigger.py` | 本地和云端共用的Python调用示例 |
| `TT-Trigger-Chrome-4.0.1.zip` | Chrome Manifest V3插件 |
| `TT-Trigger-Windows-Local-4.0.1.zip` | localhost/Tailscale Windows本地服务 |
| `TT-Trigger-Cloudflare-4.0.1.zip` | Cloudflare Worker与Durable Objects自部署源码 |

按使用方案下载：

| 使用方案 | 必须下载 | 可选下载 |
|---|---|---|
| 使用开发者提供的Cloudflare中继 | Chrome插件包 | `invoke-trigger.py`，用于Python调用 |
| 自己部署Cloudflare中继 | Chrome插件包 + Cloudflare部署包 | `invoke-trigger.py`，用于Python调用 |
| Windows localhost/Tailscale本地模式 | Chrome插件包 + Windows本地部署包 | `invoke-trigger.py`；本地包已自带PowerShell调用示例 |
| 仅更新Chrome插件 | Chrome插件包 | 无 |

每个主要文件旁边都有同名 `.sha256` 校验文件。不再发布历史版本目录、解压后的构建目录或包含重复组件的整合包。

## 安装 Chrome 插件

1. 下载并解压 `TT-Trigger-Chrome-4.0.1.zip`。
2. 打开 `chrome://extensions`，启用“开发者模式”。
3. 点击“加载已解压的扩展程序”，选择包含 `manifest.json` 的解压目录。
4. 点击 TT Trigger 图标，在弹窗选择本地或云端模式。

最低支持 Chrome 116。插件配置和E2EE密钥只存放在 `chrome.storage.local`，不会使用Chrome同步。

## 方式一：localhost / Tailscale

### 启动

1. 下载并完整解压 `TT-Trigger-Windows-Local-4.0.1.zip`。
2. 进入解压后的目录，运行 `start.bat`。首次运行会生成唯一的 `config.json`。
3. 另行安装Chrome插件，选择“本地 / Tailscale”。
4. 将 `config.json` 中的 `extension_token` 填入插件，点击“保存并连接”。
5. 插件显示“已连接”后即可调用。

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
python3 invoke-trigger.py --symbol BTC
python3 invoke-trigger.py --symbol "BG-P:SIREN/USDT+OD-S:SIREN/USDT" --add-pair
```

调用Tailscale地址时覆盖Base URL：

```powershell
python3 invoke-trigger.py --base-url http://100.100.20.30:8788 --symbol BTC
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
2. 插件默认填写公共中继 `https://tt-trigger.jwyhyc.workers.dev`，也可以覆盖为开发者提供或自部署的 `https://*.workers.dev` 地址。
3. 默认公共中继和其他开发者共享中继需要一次性激活码；自部署首次设备模式可留空。
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

另行下载 `invoke-trigger.py`，把它和插件导出的JSON放在同一目录。先安装AES-GCM依赖：

```powershell
python3 -m pip install "cryptography>=43,<47"
```

脚本会自动读取目录中唯一的TT-Trigger JSON文件，无需传入 `--config`：

```powershell
python3 invoke-trigger.py --symbol BTC
python3 invoke-trigger.py --symbol "BG-P:SIREN/USDT+OD-S:SIREN/USDT" --add-pair
```

`config.example.json`和无关JSON会被忽略。如果同目录存在多个有效的TT-Trigger配置，脚本会要求使用 `--config 文件名` 明确选择。

只有云端模式依赖 `cryptography`；本地模式仍只使用Python标准库。

### 自部署Cloudflare Worker

下载 `TT-Trigger-Cloudflare-4.0.1.zip` 并解压，或者直接使用仓库中的 [`cloudflare/`](cloudflare/README.md)。完整步骤见 **[Cloudflare Relay部署指南](cloudflare/README.md)**。

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

### 公用中继管理员设置

如果中继只供自己使用，可以保留默认的 `first_device` 模式。准备让其他用户注册时，推荐使用“一台Chrome设备一个一次性激活码”的受控注册方式。

#### 1. 设置管理员Token

使用 `deploy.ps1` 部署时，脚本会自动生成并写入管理员Token；请保存脚本最后显示的值。通过Cloudflare连接GitHub仓库部署时，需要在Cloudflare控制台中手动设置：

```text
Workers & Pages → tt-trigger-relay → Settings → Variables and Secrets → Add
Name: ADMIN_TOKEN
Type: Secret（加密）
Value: 至少32字节的随机Base64URL字符串
```

Linux/macOS生成命令：

```bash
openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n'
```

Windows PowerShell生成命令：

```powershell
$bytes = New-Object byte[] 32
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($bytes)
$rng.Dispose()
[Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
```

把生成值同时保存到密码管理器。`ADMIN_TOKEN` 只提供给中继管理员，不要填写到Chrome插件，不要发送给普通用户，也不要写进Git仓库。

#### 2. 启用激活码注册

将 `cloudflare/wrangler.toml` 中的注册模式改为：

```toml
[vars]
ENROLLMENT_MODE = "activation_required"
```

保留同一 `[vars]` 中的超时、限流等其他配置，然后提交到GitHub等待Cloudflare重新部署，或者在 `cloudflare` 目录手动部署：

```bash
npm install
npx wrangler deploy
```

切换注册模式不会删除已经注册的设备和调用方。公用中继不要设置 `OPEN_ENROLLMENT=true`；该选项会允许任何知道中继地址的人注册。

#### 3. 生成一次性激活码

进入仓库或Cloudflare部署包的 `cloudflare` 目录，设置管理命令需要的环境变量。

Linux/macOS：

```bash
export TT_RELAY_URL='https://relay.example.workers.dev'
export TT_ADMIN_TOKEN='保存在密码管理器中的管理员Token'

# 生成10个、24小时有效的激活码
npm run admin -- codes 10 86400
```

Windows PowerShell：

```powershell
$env:TT_RELAY_URL='https://relay.example.workers.dev'
$env:TT_ADMIN_TOKEN='保存在密码管理器中的管理员Token'

# 生成10个、24小时有效的激活码
npm run admin -- codes 10 86400
```

命令格式为：

```text
npm run admin -- codes <数量> <有效秒数>
```

例如 `codes 1 3600` 表示生成1个、1小时有效的激活码。每个激活码只能成功注册一台Chrome插件设备，使用后立即失效，过期后也不能再使用。

#### 4. 提供给普通用户

每位用户只需要获得以下内容：

1. `TT-Trigger-Chrome-4.0.1.zip`；
2. `invoke-trigger.py`；
3. 开发者部署的Worker根地址；
4. 一个尚未使用的一次性激活码。

用户安装插件后选择“云端 E2EE”，填写Worker地址和激活码，点击“注册并连接”，然后在插件中“新增并导出”自己的调用方JSON。激活码输入框只在尚未注册云端设备时显示；已经注册并连接的插件不会再次显示该输入框。

用户导出的JSON包含其独立的调用Token和E2EE密钥，不应交给中继管理员或其他用户。管理员Token也绝不能提供给用户。

#### 5. 吊销整台设备

需要封禁某台设备时，取得该设备的 `device_id`，并在已经设置上述环境变量的终端运行：

```bash
npm run admin -- revoke DEVICE_ID
```

设备吊销后，其插件连接和全部调用方JSON都会失效。单个调用方的轮换或吊销由用户在插件页面完成。

完整的管理员部署、激活码和故障排查步骤见 [Cloudflare Relay部署指南](cloudflare/README.md)。

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
VERSION=4.0.1 ./scripts/build-release.sh
```

发布产物：

```text
dist/invoke-trigger.py
dist/invoke-trigger.py.sha256
dist/TT-Trigger-Chrome-4.0.1.zip
dist/TT-Trigger-Chrome-4.0.1.zip.sha256
dist/TT-Trigger-Windows-Local-4.0.1.zip
dist/TT-Trigger-Windows-Local-4.0.1.zip.sha256
dist/TT-Trigger-Cloudflare-4.0.1.zip
dist/TT-Trigger-Cloudflare-4.0.1.zip.sha256
```
