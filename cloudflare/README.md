# TT-Trigger Cloudflare Relay 部署指南

本目录是 TT-Trigger 的 Cloudflare Workers + Durable Objects 中继。`symbol`、`addPair` 和网页执行结果均在调用方与Chrome插件之间端到端加密，Worker只负责认证、在线转发和限流。

## 一、准备工作

部署前需要：

1. 一个可登录的 [Cloudflare账户](https://dash.cloudflare.com/sign-up)。免费套餐即可。
2. Windows 10/11。
3. 安装 [Node.js LTS](https://nodejs.org/)，建议Node.js 20或更高版本。
4. 下载并完整解压 `TT-Trigger-4.0.0-cloudflare.zip`，或者克隆本仓库。

打开PowerShell检查环境：

```powershell
node --version
npm --version
```

两个命令都能显示版本号后再继续。

## 二、推荐部署：Windows引导脚本

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
npm install
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

## 三、在Chrome插件中注册第一个设备

默认配置使用 `first_device` 模式：不需要激活码，但只允许第一个设备完成注册。

部署成功后应立即操作：

1. 安装并打开TT-Trigger 4.0 Chrome插件。
2. 选择 **云端 E2EE**。
3. 在“Cloudflare中继地址”填写完整Worker URL，例如：
   ```text
   https://tt-trigger-relay.YOUR-SUBDOMAIN.workers.dev
   ```
4. “激活码”留空。
5. 点击 **注册并连接**。
6. Chrome弹出站点访问权限时点击允许。
7. 等待插件顶部显示 **已连接**。

第一个设备注册成功后，Worker会原子关闭无激活码注册。刷新插件或网络断线重连不会重复占用名额。

接着创建调用方：

1. 在“新增调用方”输入名称，例如“交易机器人”。
2. 点击 **新增并导出**。
3. 妥善保存下载的JSON文件。
4. 使用该文件调用：
   ```powershell
   python invoke-trigger.py --config C:\path\client.json --symbol BTC
   ```

## 四、以后增加其他设备：激活码模式

这里的“设备”指一套独立Chrome插件安装。一个设备下增加多个Python调用方不需要激活码，直接在插件中点击“新增并导出”即可。

如果确实需要注册第二个Chrome设备：

### 1. 修改注册模式

编辑 `wrangler.toml`：

```toml
[vars]
ENROLLMENT_MODE = "activation_required"
```

保留文件中的其他变量，然后重新部署：

```powershell
npx wrangler deploy
```

### 2. 设置管理环境变量

使用部署脚本最后输出的管理员Token：

```powershell
$env:TT_RELAY_URL = 'https://tt-trigger-relay.YOUR-SUBDOMAIN.workers.dev'
$env:TT_ADMIN_TOKEN = '部署时保存的管理员Token'
```

### 3. 生成一次性激活码

生成1个、24小时有效的激活码：

```powershell
npm run admin -- codes 1 86400
```

生成10个、1小时有效的激活码：

```powershell
npm run admin -- codes 10 3600
```

将输出的其中一个激活码交给新设备用户。每个激活码只能成功使用一次，过期或重复使用都会被拒绝。

## 五、吊销整个设备

插件弹窗中的“吊销”只吊销单个Python调用方。管理员需要封禁整个设备时，先从插件或调用方文件取得 `device_id`，然后运行：

```powershell
$env:TT_RELAY_URL = 'https://tt-trigger-relay.YOUR-SUBDOMAIN.workers.dev'
$env:TT_ADMIN_TOKEN = '部署时保存的管理员Token'
npm run admin -- revoke DEVICE_ID
```

设备被吊销后，其插件连接和所有调用方都会失效。

## 六、开放注册（仅适合私有测试）

如需允许任何知道Worker地址的人注册，在 `wrangler.toml` 的 `[vars]` 中增加：

```toml
OPEN_ENROLLMENT = "true"
```

然后运行：

```powershell
npx wrangler deploy
```

恢复受控注册时删除该变量并重新部署。开发者共享服务应使用 `activation_required`，不要开启开放注册。

## 七、手动部署或更新

已经完成Wrangler登录和Secret设置后，日常更新只需：

```powershell
cd C:\path\TT-Trigger\cloudflare
npm install
npm test
npm run typecheck
npx wrangler deploy
```

更新Worker不会删除已有设备、调用方或激活码。Durable Object数据独立保留。

Linux/macOS也可部署：

```bash
cd cloudflare
npm install
npx wrangler login
npx wrangler deploy
ADMIN_TOKEN="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
printf '%s' "$ADMIN_TOKEN" | npx wrangler secret put ADMIN_TOKEN
printf 'Administrator token: %s\n' "$ADMIN_TOKEN"
```

请立即把输出的管理员Token保存到密码管理器，然后从Shell历史和环境中清除。

## 八、常见问题

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

### 首个设备被错误占用

将模式改成 `activation_required` 并重新部署，然后通过管理员CLI生成激活码。无需删除或重建Worker。

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

## 九、免费额度相关默认值

- 单请求最大8KB。
- 每设备最多20个调用方。
- 每设备每分钟60次请求，允许额外突发10次。
- 每设备同一时间只执行一个请求。
- 插件响应超时为10秒。
- 插件离线时立即返回503，不保存或排队密文。

这些值可在 `wrangler.toml` 中调整后重新部署。
