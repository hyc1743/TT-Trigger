# TT-Trigger Cloudflare Relay 部署指南

本目录是 TT-Trigger 的 Cloudflare Workers + Durable Objects 中继。`symbol`、`addPair` 和网页执行结果均在调用方与Chrome插件之间端到端加密，Worker只负责认证、在线转发和限流。

## 一、准备工作

部署前需要：

1. 一个可登录的 [Cloudflare账户](https://dash.cloudflare.com/sign-up)。免费套餐即可。
2. Windows 10/11。
3. 安装 [Node.js LTS](https://nodejs.org/)，建议Node.js 20或更高版本。
4. 下载并完整解压 `TT-Trigger-Cloudflare-4.0.2.zip`，或者克隆本仓库。

打开PowerShell检查环境：

```powershell
node --version
npm --version
```

两个命令都能显示版本号后再继续。

## 二、Cloudflare连接GitHub仓库直接部署

TT-Trigger的Worker项目位于仓库的 `cloudflare/` 子目录。Cloudflare不能在仓库根目录执行默认部署，否则Wrangler找不到 `cloudflare/wrangler.toml`，会把项目误判成静态站点并显示：

```text
Could not detect a directory containing static files
```

在Cloudflare导入GitHub仓库时，构建配置必须填写：

| 配置项 | 填写内容 |
|---|---|
| Production branch | `main` |
| Root directory | `cloudflare` |
| Build command | `npm run typecheck` |
| Deploy command | `npx wrangler deploy` |
| Build output directory | 留空 |

其中最重要的是 **Root directory = `cloudflare`**。设置后，依赖安装、构建命令和部署命令都会在正确的Worker目录执行。

### 已经创建了失败的部署

进入Cloudflare控制台：

1. 打开 **Workers & Pages**。
2. 选择刚才连接GitHub创建的项目。
3. 打开 **Settings → Builds → Build configuration**。
4. 点击编辑，将 **Root directory** 改为 `cloudflare`。
5. 将Build command改为 `npm run typecheck`。
6. 确认Deploy command为 `npx wrangler deploy`。
7. Build output directory保持空白。
8. 保存后进入 **Deployments**，点击 **Retry deployment / Redeploy**。

如果控制台不允许修改Root directory，就删除这次失败的Worker构建项目，重新选择 **Import a repository**，并在首次配置页面展开高级设置后填写上述内容。

### 无法设置Root directory时的备用命令

如果Cloudflare界面没有Root directory字段，可以继续使用仓库根目录，但把命令改为：

```text
Build command:
cd cloudflare && npm ci && npm run typecheck

Deploy command:
cd cloudflare && npx wrangler deploy
```

两种配置只能选一种。已经设置 `Root directory = cloudflare` 时，命令中不要再执行 `cd cloudflare`。

### GitHub直连部署成功后

部署日志应出现类似内容：

```text
Your Worker has access to the following bindings:
env.ENROLLMENT_REGISTRY (EnrollmentRegistry)  Durable Object
env.DEVICE_RELAY (DeviceRelay)                Durable Object
Deployed tt-trigger-relay
```

然后访问：

```text
https://你的Worker地址/health
```

GitHub直连不会运行本目录的 `deploy.ps1`，所以不会自动创建管理员Token。默认注册模式是关闭式的 `activation_required`；注册任何设备前都必须在Worker的 **Settings → Variables and Secrets** 中新增一个加密Secret：

```text
Name: ADMIN_TOKEN
Value: 恰好32字节随机数据编码成的无填充Base64URL字符串（43字符）
```

可以在本机PowerShell生成：

```powershell
$bytes = New-Object byte[] 32
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($bytes)
$rng.Dispose()
[Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
```

保存输出值到密码管理器，并把相同值作为Cloudflare加密Secret `ADMIN_TOKEN`。不要把它配置成普通明文变量。

随后设置 `TT_RELAY_URL`、`TT_ADMIN_TOKEN` 并运行 `npm run admin -- codes 1 3600`，使用输出的一次性激活码注册首个设备。

## 三、推荐部署：Windows引导脚本

### 1. 进入部署目录

如果使用独立Cloudflare压缩包：

```powershell
cd C:\你解压的位置\cloudflare
```

如果使用完整Git仓库：

```powershell
cd C:\path\TT-Trigger\cloudflare
```

确认当前目录中存在 `package.json`、`wrangler.toml` 和 `deploy.ps1`。

### 2. 安装部署工具

```powershell
npm ci
```

依赖只用于部署和测试，不会安装到Chrome插件中。

### 3. 运行部署脚本

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy.ps1
```

脚本会依次完成：

1. 打开浏览器，要求登录并授权Wrangler访问Cloudflare账户。
2. 本地生成一个随机管理员Token。
3. 创建或更新Worker以及两个Durable Object。
4. 将管理员Token安全写入已创建Worker的Secret。
5. 输出Worker地址和管理员Token。
6. 生成并输出首个设备的一次性激活码。

首次登录时，浏览器授权完成后返回PowerShell等待部署结束。Cloudflare首次使用Workers时可能要求创建 `workers.dev` 子域，按页面提示确认即可。

部署成功时会看到类似输出：

```text
Uploaded tt-trigger-relay
https://tt-trigger-relay.YOUR-SUBDOMAIN.workers.dev
管理员 Token（请妥善保存）: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

请保存：

- Worker URL：之后填写到Chrome插件。
- 管理员Token：以后生成激活码或吊销整个设备时使用；Cloudflare不会再次显示该Secret。

不要把管理员Token填入插件，也不要放入调用方 `client.json`。

### 4. 检查部署结果

将示例URL替换成部署输出的实际地址：

```powershell
Invoke-RestMethod https://tt-trigger-relay.YOUR-SUBDOMAIN.workers.dev/health
```

正常结果：

```text
ok service          version
-- -------          -------
True tt-trigger-relay       1
```

如果出现404，请确认使用的是Worker根地址，而不是Cloudflare控制台地址，也不要在URL后添加 `/v1` 或 `/extension`。

## 四、在Chrome插件中注册第一个设备

默认配置使用 `activation_required` 模式。Windows部署脚本会在管理员Secret设置完成后生成一个一小时有效的一次性激活码。

部署成功后应立即操作：

1. 安装并打开TT-Trigger 4.0 Chrome插件。
2. 选择 **云端 E2EE**。
3. 在“Cloudflare中继地址”填写完整Worker URL，例如：
   ```text
   https://tt-trigger-relay.YOUR-SUBDOMAIN.workers.dev
   ```
4. 填写部署脚本输出的首个设备激活码。
5. 点击 **注册并连接**。
6. Chrome弹出站点访问权限时点击允许。
7. 等待插件顶部显示 **已连接**。

激活码使用后立即失效。刷新插件或网络断线重连使用已有设备凭据，不需要新激活码。

如需更换扩展目录或Chrome配置文件，请先从云端设备面板导出加密设备备份。备份使用用户提供的密码加密，并包含恢复同一注册设备所需的Token、Grant和调用方密钥。

接着创建调用方：

1. 在“新增调用方”输入名称，例如“交易机器人”。
2. 点击 **新增并导出**。
3. 妥善保存下载的JSON文件。
4. 把导出的JSON与 `invoke-trigger.py` 放在同一目录，直接调用：
   ```powershell
   python3 invoke-trigger.py --symbol BTC
   ```

脚本会自动识别同目录唯一的TT-Trigger JSON文件；只有存在多个有效配置时才需要添加 `--config 文件名`。

## 五、以后增加其他设备：激活码模式

这里的“设备”指一套独立Chrome插件安装。一个设备下增加多个Python调用方不需要激活码，直接在插件中点击“新增并导出”即可。

如果确实需要注册第二个Chrome设备：

### 1. 设置管理环境变量

使用部署脚本最后输出的管理员Token：

```powershell
$env:TT_RELAY_URL = 'https://tt-trigger-relay.YOUR-SUBDOMAIN.workers.dev'
$env:TT_ADMIN_TOKEN = '部署时保存的管理员Token'
```

### 2. 生成一次性激活码

生成1个、24小时有效的激活码：

```powershell
npm run admin -- codes 1 86400
```

生成10个、1小时有效的激活码：

```powershell
npm run admin -- codes 10 3600
```

将输出的其中一个激活码交给新设备用户。每个激活码只能成功使用一次，过期或重复使用都会被拒绝。

## 六、吊销整个设备

插件弹窗中的“吊销”只吊销单个Python调用方。管理员需要封禁整个设备时，先从插件或调用方文件取得 `device_id`，然后运行：

```powershell
$env:TT_RELAY_URL = 'https://tt-trigger-relay.YOUR-SUBDOMAIN.workers.dev'
$env:TT_ADMIN_TOKEN = '部署时保存的管理员Token'
npm run admin -- revoke DEVICE_ID
```

设备被吊销后，其插件连接和所有调用方都会失效。

## 七、开放注册（仅适合私有测试）

如需允许任何知道Worker地址的人注册，在 `wrangler.toml` 的 `[vars]` 中增加：

```toml
OPEN_ENROLLMENT = "true"
```

然后运行：

```powershell
npx wrangler deploy
```

恢复受控注册时删除该变量并重新部署。开发者共享服务应使用 `activation_required`，不要开启开放注册。

## 八、手动部署或更新

已经完成Wrangler登录和Secret设置后，日常更新只需：

```powershell
cd C:\path\TT-Trigger\cloudflare
npm ci
npm test
npm run typecheck
npx wrangler deploy
```

更新Worker不会删除已有设备、调用方或激活码。Durable Object数据独立保留。

从4.0.1升级时，先部署Worker，再原目录覆盖Chrome扩展并点击“重新加载”。Worker默认兼容旧WebSocket认证，因此不需要重新注册。若旧 `ADMIN_TOKEN` 不是43字符的32字节Base64URL值，请覆盖为新随机值；这只影响管理命令，不影响设备连接。

Linux/macOS也可部署：

```bash
cd cloudflare
npm ci
npx wrangler login
npx wrangler deploy
ADMIN_TOKEN="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
printf '%s' "$ADMIN_TOKEN" | npx wrangler secret put ADMIN_TOKEN
printf 'Administrator token: %s\n' "$ADMIN_TOKEN"
export TT_RELAY_URL='https://你的Worker地址'
export TT_ADMIN_TOKEN="$ADMIN_TOKEN"
npm run admin -- codes 1 3600
```

请立即把输出的管理员Token保存到密码管理器，然后从Shell历史和环境中清除。

## 九、常见问题

### Wrangler登录后没有继续

回到原PowerShell窗口等待；必要时按一次Enter。也可运行：

```powershell
npx wrangler whoami
```

确认当前登录账户。

### `workers.dev` 地址无法访问

进入Cloudflare控制台的 **Workers & Pages → tt-trigger-relay → Settings → Domains & Routes**，确认 `workers.dev` 路由已启用。

### 插件提示无法连接

依次检查：

1. `/health` 是否能访问。
2. 插件填写的是否是纯HTTPS根地址。
3. Chrome是否授予了该Worker地址的访问权限。
4. 插件是否已完成注册，而不是只保存了地址。
5. Cloudflare控制台中Worker是否处于已部署状态。

### 旧扩展升级后无法连接

Worker默认暂时保留 `ALLOW_LEGACY_WS_AUTH = "true"`，旧扩展仍可连接。新版扩展会自动使用一次性WebSocket ticket。确认所有扩展已更新后，可将该变量改为 `"false"` 并重新部署；这不会删除设备数据。

### 忘记管理员Token

重新生成一个随机值并覆盖Secret：

```powershell
$bytes = New-Object byte[] 32
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($bytes)
$rng.Dispose()
$admin = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
$admin | npx wrangler secret put ADMIN_TOKEN
$admin
```

保存输出的新Token。覆盖管理员Token不会影响现有插件或调用方。

## 十、免费额度相关默认值

- Chrome扩展先通过已认证的 `POST /v1/devices/{deviceId}/extension-ticket` 获取30秒、一次性的WebSocket ticket；长期设备Token不会写入WebSocket URL。
- `ALLOW_LEGACY_WS_AUTH = "true"` 仅用于旧扩展滚动升级，完成升级后可关闭。
- 单请求最大8KB。
- 每设备最多20个调用方。
- 每调用方每分钟60次请求，允许额外突发10次。
- 每设备合计每分钟240次请求，允许额外突发40次。
- 每设备同一时间只执行一个请求。
- 插件响应超时为10秒。
- 插件离线时立即返回503，不保存或排队密文。

这些值可在 `wrangler.toml` 中调整后重新部署。
