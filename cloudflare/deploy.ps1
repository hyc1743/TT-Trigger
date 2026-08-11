$ErrorActionPreference = 'Stop'
$bytes = New-Object byte[] 32
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($bytes)
$rng.Dispose()
$admin = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
Write-Host 'Cloudflare 登录后将自动部署 TT-Trigger Relay。'
npx wrangler login
$admin | npx wrangler secret put ADMIN_TOKEN
npx wrangler deploy
Write-Host "管理员 Token（请妥善保存）: $admin"
Write-Host '默认 first_device 模式允许第一个插件注册，成功后自动关闭注册。'
