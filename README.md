# TT-Trigger 4.0

TT-Trigger 可以从外部程序触发 Chrome，在当前 `https://taoli.tools/*` 页面填写交易对，并可等待 1000ms 后点击 **Add Pair / 添加交易对**。

支持两种互斥的连接方式：

- **本地 / Tailscale**：Windows 本地服务接收带时间戳、nonce 和 HMAC-SHA256 的 `POST /webhook`。
- **Cloudflare 云端 E2EE**：Chrome 插件直接连接 Cloudflare Worker；请求与执行结果只在调用方和插件中解密，不需要在电脑上运行本地服务。

## ⚠️ 公共中继隐私政策（使用前必读）

> [!IMPORTANT]
> 插件默认填写的公共中继是 `https://tt-trigger.jwyhyc.workers.dev`。E2EE保护业务正文，但**不会阻止Cloudflare在网络层处理来源IP、连接时间或稳定的随机设备标识**。当前TT-Trigger Worker不记录IP；如果连Cloudflare平台也不希望接触真实出口IP，请使用本地/Tailscale模式，或在Chrome和Python两端使用VPN/代理。
>
> **当前公共中继的TT-Trigger Worker没有应用级IP日志。** 当前公开代码没有读取或输出 `CF-Connecting-IP`，没有将Python调用端或Chrome设备的IP写入Durable Objects，`wrangler.toml` 的日志采样率为 `head_sampling_rate = 0`，也没有管理员查询历史IP的接口。因此，中继部署者不能通过当前TT-Trigger程序查看或追溯用户历史IP；但Cloudflare平台仍会在网络层处理来源IP，并可能因安全事件、账户功能或平台策略保留相关元数据。

以下说明针对本仓库v4.0.2公开实现及其默认配置。中继部署者在技术上可以修改Worker代码，因此“当前未记录”不等于部署者永远没有记录能力。

### 当前中继管理员实际能从TT-Trigger看到什么

这里必须区分“当前TT-Trigger程序实际记录的数据”和“Cloudflare平台在网络层有能力处理的数据”。按当前公开代码和默认配置，中继管理员通过TT-Trigger管理命令、Worker应用日志或Durable Objects得到的情况如下：

| 信息 | 当前TT-Trigger中的实际情况 |
|---|---|
| Python调用方出口IP | **不读取、不记录，无法通过TT-Trigger查询历史IP** |
| Chrome设备出口IP | **不读取、不记录，无法通过TT-Trigger查询历史IP** |
| `device_id`、`key_id` | 随机标识；为了路由和认证会被处理，其中注册信息会持久保存 |
| 在线状态 | Worker转发时可判断插件当前是否在线，但不保存完整在线历史 |
| 请求频率 | 保存当前限流窗口的计数，不保存完整调用历史 |
| E2EE密文 | 仅在请求转发期间临时处理，不持久保存正文 |
| 激活与吊销状态 | 保存激活码哈希、设备/调用方认证哈希及吊销状态 |

因此，**当前公共中继管理员不能从TT-Trigger看到Python调用端或Chrome设备的当前/历史出口IP**。`device_id` 和 `key_id` 是随机标识，不直接包含姓名或IP，但中继必须用它们完成设备路由和调用方认证。

### IP在网络层如何处理

Python的 `/trigger` HTTPS请求和Chrome插件的 `/extension` WebSocket都会先到达Cloudflare。Cloudflare基础设施因此会处理对应的公网出口IP；使用VPN或代理时，Cloudflare处理的是VPN/代理出口IP。通常不会由此得到 `192.168.x.x` 等局域网IP或Tailscale私有IP。

Cloudflare会把 `CF-Connecting-IP` 提供给Worker运行环境，但当前TT-Trigger代码没有读取、输出或保存它。这意味着：

- “Cloudflare在网络层接收了IP”不等于“当前中继管理员在TT-Trigger日志里看到了IP”；
- 当前没有TT-Trigger应用日志或管理页面可以查询这些IP；
- Cloudflare自己的安全事件、聚合分析或平台日志仍可能按账户功能和平台策略处理部分网络元数据；
- 部署者只有在以后修改Worker代码或日志配置后，才可能通过TT-Trigger开始记录修改之后的新IP；
- 后续开启日志不能恢复此前从未由TT-Trigger保存的历史IP。

### 中继无法从E2EE内容中看到什么

在调用方JSON和Chrome插件没有泄漏、密码学实现未被绕过的前提下，Worker不会取得以下明文：

| 不可见内容 | 保护方式 |
|---|---|
| `symbol` | AES-256-GCM端到端加密并固定长度填充 |
| `addPair` | 与请求正文一起端到端加密 |
| 页面执行结果和错误详情 | 由插件加密后返回 |
| 调用方E2EE `secret` | 只在插件端生成并写入用户导出的JSON，不发送给Worker |
| AES/HMAC派生密钥 | 只在Python调用端和插件端派生 |

部署者可以拒绝、延迟或中断请求，也可以吊销设备，但没有E2EE Secret时不能伪造通过调用方验证的加密响应。密文保护不代表匿名：调用时间和设备标识仍可能被关联分析。

### 当前TT-Trigger程序记录什么

为了完成认证、吊销和限流，Durable Objects会持久保存：

- 随机 `device_id` 及设备Token/Grant的SHA-256哈希；
- 随机 `key_id` 及调用方Token/Grant的SHA-256哈希；
- 设备吊销状态；
- 激活码哈希和有效期；
- 最多10分钟的注册重试摘要及最多30秒的一次性WebSocket ticket哈希；
- 每台设备及每个调用方的限流计数和时间窗口。

这些数据不包含E2EE Secret和业务正文。吊销单个调用方时会删除对应调用方记录；吊销整台设备时会保留带有吊销标记的设备记录，以阻止其重新连接。已使用的激活码哈希会立即删除；过期但未使用的激活码和过期注册重试摘要由Durable Object alarm自动清理。

### 当前TT-Trigger程序没有主动记录什么

公开实现中没有应用级访问日志或IP数据库，`head_sampling_rate` 默认为 `0`，并且不会主动持久保存：

- Python调用方或Chrome设备的完整IP地址；
- `symbol`、`addPair`或页面执行结果；
- 请求/响应密文正文；
- 原始设备Token、Relay Token、Grant或E2EE Secret；
- IP与 `device_id`、`key_id` 的历史对应关系；
- 离线请求队列或完整调用历史。

Cloudflare运行环境提供的请求对象可能包含IP请求头，但当前TT-Trigger代码不会读取该IP字段。代码只临时处理路由所需的随机标识、认证Token/Grant和密文，并且不会把它们作为上述明文日志持久化。

具体而言，当前公共中继配置和代码满足以下条件：

- 没有读取或输出 `CF-Connecting-IP`；
- 没有通过 `console.log()` 记录Python调用端或Chrome设备IP；
- 没有将IP写入Durable Objects；
- 没有保存IP与 `device_id`、`key_id` 的历史关联；
- 没有向管理员提供IP查询、搜索或导出接口；
- `head_sampling_rate = 0`，不采样TT-Trigger应用的Worker可观测性日志。

所以，在保持当前部署代码和配置不变的情况下，公共中继管理员无法从TT-Trigger中查询当前或历史用户IP。以后即使开始记录，也不能通过TT-Trigger恢复此前从未保存的历史IP。

### Cloudflare平台边界

即使TT-Trigger代码不记录IP，Cloudflare作为TLS终止和Workers运行平台仍然能够处理来源IP、请求时间、路由、流量和安全事件。Cloudflare控制台可能提供聚合分析或安全事件，具体保留范围取决于Cloudflare套餐、账户设置和Cloudflare隐私政策，TT-Trigger无法提供“Cloudflare完全不留存”的保证。

公共中继管理员Token只能生成激活码和吊销已知设备，不能解密E2EE正文。管理员Token、插件存储或用户导出的调用方JSON一旦泄漏，会产生不同程度的控制风险，必须分别妥善保管。

示例 `symbol`：

```text
BG-P:SIREN/USDT+OD-S:SIREN/USDT
```

## 发布文件与下载选择

每次发布只提供以下4个主要文件，互相独立：

| 文件 | 内容 |
|---|---|
| `invoke-trigger.py` | 本地和云端共用的Python调用示例 |
| `TT-Trigger-Chrome-4.0.2.zip` | Chrome Manifest V3插件 |
| `TT-Trigger-Windows-Local-4.0.2.zip` | localhost/Tailscale Windows本地服务 |
| `TT-Trigger-Cloudflare-4.0.2.zip` | Cloudflare Worker与Durable Objects自部署源码 |

按使用方案下载：

| 使用方案 | 必须下载 | 可选下载 |
|---|---|---|
| 使用开发者提供的Cloudflare中继 | Chrome插件包 | `invoke-trigger.py`，用于Python调用 |
| 自己部署Cloudflare中继 | Chrome插件包 + Cloudflare部署包 | `invoke-trigger.py`，用于Python调用 |
| Windows localhost/Tailscale本地模式 | Chrome插件包 + Windows本地部署包 | `invoke-trigger.py`；本地包已自带PowerShell调用示例 |
| 仅更新Chrome插件 | Chrome插件包 | 无 |

每个主要文件旁边都有同名 `.sha256` 校验文件。不再发布历史版本目录、解压后的构建目录或包含重复组件的整合包。

## 安装 Chrome 插件

1. 下载并解压 `TT-Trigger-Chrome-4.0.2.zip`。
2. 打开 `chrome://extensions`，启用“开发者模式”。
3. 点击“加载已解压的扩展程序”，选择包含 `manifest.json` 的解压目录。
4. 点击 TT Trigger 图标，在弹窗选择本地或云端模式。

升级已经注册的解压版插件时，请覆盖原扩展目录中的文件并在 `chrome://extensions` 点击“重新加载”。不要删除旧扩展后从新目录加载，否则Chrome可能分配新的扩展ID，原有设备凭据不会自动迁移。

云端设备面板可导出使用PBKDF2-SHA256和AES-256-GCM保护的加密设备备份。必须更换扩展目录、Chrome配置文件或电脑时，先导出备份并将备份密码单独保存，再在新扩展中导入；备份包含设备和全部调用方凭据。

最低支持 Chrome 116。插件配置和E2EE密钥只存放在 `chrome.storage.local`，不会使用Chrome同步。

## 方式一：localhost / Tailscale

### 启动

1. 下载并完整解压 `TT-Trigger-Windows-Local-4.0.2.zip`。
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

Tailscale模式仍应在Tailnet ACL/Grants中只允许实际调用设备访问目标Windows节点的TCP 8788。HMAC不能替代Tailnet的最小权限策略，也不要通过端口转发把该HTTP端口暴露到普通LAN或公网。

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
每个本地HMAC key默认每分钟最多60次请求并允许10次突发；Tailscale来源还会受到独立的来源限流。可在 `config.json` 中调整 `webhook_rate_limit_per_minute` 和 `webhook_rate_limit_burst`。

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
3. 默认公共中继和自部署中继均需要一次性激活码；部署脚本会在设置管理员Secret后生成首个一小时有效的激活码。
4. 点击“注册并连接”，按Chrome提示授予该中继Origin的访问权限。
5. 在“新增调用方”输入名称，点击“新增并导出”，保存下载的调用方JSON。

每个调用方拥有独立中继Token和独立E2EE密钥。新调用方默认只能填写symbol，可按需勾选允许点击Add Pair。轮换会先创建并导出新凭据，再吊销旧凭据；只有界面显示“轮换完成”后旧文件才确认失效。

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

下载 `TT-Trigger-Cloudflare-4.0.2.zip` 并解压，或者直接使用仓库中的 [`cloudflare/`](cloudflare/README.md)。完整步骤见 **[Cloudflare Relay部署指南](cloudflare/README.md)**。

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
npm ci
powershell -ExecutionPolicy Bypass -File .\deploy.ps1
```

部署期间浏览器会要求登录Cloudflare。成功后保存PowerShell输出的：

1. `https://*.workers.dev` Worker URL；
2. 管理员Token。

先检查服务：

```powershell
Invoke-RestMethod https://你的Worker地址/health
```

部署脚本会输出Worker根地址、管理员Token和首个设备激活码。然后在插件中选择“云端 E2EE”，填写Worker根地址和该激活码，点击“注册并连接”。默认 `activation_required` 模式不会开放无激活码注册。

### 公用中继管理员设置

默认始终使用“一台Chrome设备一个一次性激活码”的受控注册方式；这同样适用于仅供自己使用的中继，可避免Worker刚部署时被第三方抢先注册。

#### 1. 设置管理员Token

使用 `deploy.ps1` 部署时，脚本会自动生成并写入管理员Token；请保存脚本最后显示的值。通过Cloudflare连接GitHub仓库部署时，需要在Cloudflare控制台中手动设置：

```text
Workers & Pages → tt-trigger-relay → Settings → Variables and Secrets → Add
Name: ADMIN_TOKEN
Type: Secret（加密）
Value: 恰好32字节随机数据编码成的无填充Base64URL字符串（43字符）
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

#### 2. 确认激活码注册

默认 `cloudflare/wrangler.toml` 已包含：

```toml
[vars]
ENROLLMENT_MODE = "activation_required"
```

如果旧部署仍使用其他模式，保留同一 `[vars]` 中的超时、限流等其他配置并重新部署：

```bash
npm ci
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

1. `TT-Trigger-Chrome-4.0.2.zip`；
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
- 插件在执行页面操作前持久化执行中记录；无法确定前次是否完成时会拒绝重复执行，而不是再次点击Add Pair。
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
VERSION=4.0.2 ./scripts/build-release.sh
```

正式发布时可设置 `WINDOWS_SIGN_PFX`/`WINDOWS_SIGN_PASSWORD` 让构建脚本用Authenticode证书签署EXE，并设置 `COSIGN_KEY` 为四个下载文件生成Sigstore签名和证书。SHA256文件只用于完整性校验，不能替代发布者签名。

发布产物：

```text
dist/invoke-trigger.py
dist/invoke-trigger.py.sha256
dist/TT-Trigger-Chrome-4.0.2.zip
dist/TT-Trigger-Chrome-4.0.2.zip.sha256
dist/TT-Trigger-Windows-Local-4.0.2.zip
dist/TT-Trigger-Windows-Local-4.0.2.zip.sha256
dist/TT-Trigger-Cloudflare-4.0.2.zip
dist/TT-Trigger-Cloudflare-4.0.2.zip.sha256
```
