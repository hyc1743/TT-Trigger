# TT-Trigger Cloudflare Relay

Cloudflare Workers Free + Durable Objects 中继。业务请求和响应均在调用方与浏览器插件之间端到端加密。

## 推荐部署

```powershell
cd cloudflare
npm install
.\deploy.ps1
```

把部署输出的 `https://*.workers.dev` 地址填入插件。默认 `first_device` 模式不需要激活码，第一个成功注册的插件会关闭后续注册。

开发者共享服务应把 `ENROLLMENT_MODE` 改成 `activation_required`，设置 `ADMIN_TOKEN` Secret，然后使用：

```powershell
$env:TT_RELAY_URL='https://relay.example.workers.dev'
$env:TT_ADMIN_TOKEN='...'
npm run admin -- codes 10 86400
```

自用实例也可设置 `OPEN_ENROLLMENT=true`，但会允许任何知道地址的人注册设备。
