$ErrorActionPreference = 'Stop'
$bytes = New-Object byte[] 32
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($bytes)
$rng.Dispose()
$admin = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+','-').Replace('/','_')
Write-Host 'Cloudflare 登录后将自动部署 TT-Trigger Relay。'
npx wrangler login
$deployOutput = @(& npx wrangler deploy 2>&1)
$deployOutput | ForEach-Object { Write-Host $_ }
$admin | npx wrangler secret put ADMIN_TOKEN
Write-Host "管理员 Token（请妥善保存）: $admin"
$joined = $deployOutput -join "`n"
$match = [regex]::Match($joined, 'https://[A-Za-z0-9.-]+\.workers\.dev')
if ($match.Success) {
    $relay = $match.Value.TrimEnd('/')
    $headers = @{ Authorization = "Bearer $admin" }
    $firstCode = $null
    for ($attempt = 1; $attempt -le 5 -and -not $firstCode; $attempt++) {
        try {
            $firstCode = Invoke-RestMethod -Method Post -Uri "$relay/v1/admin/activation-codes" -Headers $headers `
                -ContentType 'application/json' -Body '{"count":1,"ttlSeconds":3600}'
        } catch {
            if ($attempt -eq 5) { throw }
            Start-Sleep -Seconds 2
        }
    }
    Write-Host "首个设备激活码（1小时有效）: $($firstCode.codes[0])"
    Write-Host "Worker URL: $relay"
} else {
    Write-Warning '未能自动识别Worker URL。请设置TT_RELAY_URL和TT_ADMIN_TOKEN后运行 npm run admin -- codes 1 3600。'
}
Write-Host '注册默认关闭；必须使用管理员生成的一次性激活码。'
