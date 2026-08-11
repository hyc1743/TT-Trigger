param(
    [ValidateSet('Start','Stop','Status','Configure','Keys')]
    [string]$Action = 'Start'
)

$ErrorActionPreference = 'Stop'
$HomeDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ServerExe = Join-Path $HomeDir 'tt-trigger-server.exe'
$ConfigPath = Join-Path $HomeDir 'config.json'
$DeploymentPath = Join-Path $HomeDir 'deployment.json'
$RuntimeDir = Join-Path $HomeDir 'runtime'
$LogsDir = Join-Path $HomeDir 'logs'
$ServerPidFile = Join-Path $HomeDir 'tt-trigger.pid'
$CaddyPidFile = Join-Path $HomeDir 'caddy.pid'
$CaddyVersion = '2.11.4'
$CaddyZipSha256 = '1708333f79e274c7697285afe6d592ab39314e0b131e9ec6bea08ad27df62ebf'
$CaddyUrl = "https://github.com/caddyserver/caddy/releases/download/v$CaddyVersion/caddy_${CaddyVersion}_windows_amd64.zip"

function Write-Utf8NoBom([string]$Path, [string]$Text) {
    [IO.File]::WriteAllText($Path, $Text, (New-Object Text.UTF8Encoding($false)))
}

function Get-LivePid([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return $null }
    $raw = (Get-Content -LiteralPath $Path -Raw).Trim()
    if ($raw -notmatch '^\d+$') { Remove-Item $Path -Force; return $null }
    $process = Get-Process -Id ([int]$raw) -ErrorAction SilentlyContinue
    if ($null -eq $process) { Remove-Item $Path -Force; return $null }
    return [int]$raw
}

function Stop-One([string]$PidFile, [string]$Name) {
    $processId = Get-LivePid $PidFile
    if ($null -eq $processId) { return }
    & taskkill.exe /PID $processId /T /F 2>$null | Out-Null
    Remove-Item $PidFile -Force -ErrorAction SilentlyContinue
    Write-Host "$Name stopped."
}

function Stop-All {
    Stop-One $CaddyPidFile 'Caddy'
    Stop-One $ServerPidFile 'TT-Trigger'
}

function Protect-Config {
    try {
        $identity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
        foreach ($path in @($ConfigPath, "$ConfigPath.pre-3.0.bak")) {
            if (Test-Path $path) {
                & icacls.exe $path /inheritance:r /grant:r "${identity}:(F)" /grant:r 'SYSTEM:(F)' 2>$null | Out-Null
            }
        }
    } catch {
        Write-Warning '无法收紧 config.json ACL，请确认只有当前用户可以读取该文件。'
    }
}

function Initialize-Config {
    if (-not (Test-Path $ServerExe)) { throw "未找到 $ServerExe" }
    & $ServerExe --init --config $ConfigPath
    if ($LASTEXITCODE -ne 0) { throw '创建或迁移 config.json 失败。' }
    Protect-Config
}

function Save-Deployment($Value) {
    Write-Utf8NoBom $DeploymentPath (($Value | ConvertTo-Json -Depth 4) + "`n")
}

function Configure-Mode {
    Write-Host ''
    Write-Host '请选择 TT-Trigger 运行方案：'
    Write-Host '  1. localhost + 可选 Tailscale（HTTP）'
    Write-Host '  2. 公网域名 + Caddy + Let''s Encrypt（HTTPS）'
    do { $choice = (Read-Host '输入 1 或 2').Trim() } until ($choice -in @('1','2'))
    if ($choice -eq '1') {
        Save-Deployment ([ordered]@{ mode = 'local_tailscale' })
        Write-Host '已保存方案一。'
        return
    }
    do {
        $domain = (Read-Host '输入已解析到本机公网 IP 的域名（不含 https:// 和路径）').Trim().ToLowerInvariant()
        $valid = $domain -match '^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$'
        if (-not $valid) { Write-Warning '域名格式无效。示例：trigger.example.com' }
    } until ($valid)
    $email = (Read-Host 'Let''s Encrypt 联系邮箱（可留空）').Trim()
    if ($email -and $email -notmatch '^[^\s@]+@[^\s@]+\.[^\s@]+$') { throw '邮箱格式无效。' }
    Save-Deployment ([ordered]@{ mode = 'public_caddy'; domain = $domain; acme_email = $email })
    Write-Host '已保存方案二。'
    Write-Host '请确保公网 TCP 80/443 已通过 Windows 防火墙及路由器转发到此电脑。'
}

function Load-Deployment {
    if (-not (Test-Path $DeploymentPath)) { Configure-Mode }
    try { return Get-Content $DeploymentPath -Raw | ConvertFrom-Json }
    catch { throw 'deployment.json 无法解析，请运行 configure.bat 重新配置。' }
}

function Find-TailscaleIPv4 {
    $candidates = @((Get-Command tailscale.exe -ErrorAction SilentlyContinue | ForEach-Object Source), "$env:ProgramFiles\Tailscale\tailscale.exe")
    $exe = $candidates | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
    if (-not $exe) { Write-Warning '未安装 Tailscale，仅监听 localhost。'; return $null }
    & $exe status *> $null
    if ($LASTEXITCODE -ne 0) { Write-Warning 'Tailscale 未连接，仅监听 localhost。'; return $null }
    $rawIp = & $exe ip -4 2>$null | Select-Object -First 1
    $ip = if ($rawIp) { ([string]$rawIp).Trim() } else { '' }
    if ($ip -notmatch '^100\.(?:6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.(?:\d{1,3})\.(?:\d{1,3})$') {
        Write-Warning '无法获取有效的 Tailscale IPv4，仅监听 localhost。'; return $null
    }
    return $ip
}

function Start-Server([string]$AdditionalListen) {
    New-Item -ItemType Directory -Force -Path $LogsDir | Out-Null
    $arguments = @('--config', ('"{0}"' -f $ConfigPath))
    if ($AdditionalListen) { $arguments += @('--api-listen', $AdditionalListen) }
    $p = Start-Process -FilePath $ServerExe -ArgumentList $arguments -WorkingDirectory $HomeDir -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $LogsDir 'server.log') -RedirectStandardError (Join-Path $LogsDir 'server-error.log') -PassThru
    Set-Content -LiteralPath $ServerPidFile -Value $p.Id -Encoding Ascii
    Start-Sleep -Milliseconds 700
    if ($p.HasExited) { throw 'TT-Trigger 启动失败，请查看 logs\server-error.log。' }
    try { Invoke-RestMethod -Uri 'http://127.0.0.1:8788/health' -TimeoutSec 3 | Out-Null }
    catch { throw 'TT-Trigger localhost 健康检查失败。' }
    return $p
}

function Ensure-Caddy {
    $caddyDir = Join-Path $RuntimeDir 'caddy'
    $caddyExe = Join-Path $caddyDir 'caddy.exe'
    if (Test-Path $caddyExe) {
        $installedVersion = (& $caddyExe version 2>$null | Select-Object -First 1)
        if ($installedVersion -and ([string]$installedVersion).StartsWith("v$CaddyVersion ")) { return $caddyExe }
        Write-Warning '已安装的Caddy版本不匹配，将重新下载固定版本。'
        Remove-Item $caddyExe -Force
    }
    New-Item -ItemType Directory -Force -Path $caddyDir | Out-Null
    $zip = Join-Path $RuntimeDir "caddy-$CaddyVersion.zip"
    Write-Host "正在从 GitHub 下载 Caddy v$CaddyVersion..."
    Invoke-WebRequest -UseBasicParsing -Uri $CaddyUrl -OutFile $zip
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
    if ($actual -ne $CaddyZipSha256) { Remove-Item $zip -Force; throw 'Caddy SHA-256 校验失败，已删除下载文件。' }
    Expand-Archive -LiteralPath $zip -DestinationPath $caddyDir -Force
    Remove-Item $zip -Force
    if (-not (Test-Path $caddyExe)) { throw 'Caddy 压缩包中未找到 caddy.exe。' }
    return $caddyExe
}

function Write-Caddyfile($Deployment) {
    $global = "{`n`tacme_ca https://acme-v02.api.letsencrypt.org/directory`n`tadmin 127.0.0.1:2019"
    if ($Deployment.acme_email) { $global += "`n`temail $($Deployment.acme_email)" }
    $global += "`n}`n`n"
    $site = @"
$($Deployment.domain) {
	route {
		@webhook {
			method POST
			path /webhook
		}
		reverse_proxy @webhook 127.0.0.1:8788
		respond 404
	}
}

function Test-PublicPrerequisites($Deployment) {
    try {
        $addresses = [Net.Dns]::GetHostAddresses([string]$Deployment.domain) | ForEach-Object IPAddressToString
        if (-not $addresses) { throw '没有DNS记录' }
        Write-Host "域名当前解析: $($addresses -join ', ')"
    } catch {
        throw "域名 $($Deployment.domain) 无法解析，请先配置DNS。"
    }
    $getTcp = Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue
    if ($getTcp) {
        $occupied = Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | Where-Object { $_.LocalPort -in @(80,443) }
        if ($occupied) {
            $summary = ($occupied | ForEach-Object { "port $($_.LocalPort) PID $($_.OwningProcess)" }) -join '; '
            throw "公网端口已被占用：$summary"
        }
    }
    $getFirewall = Get-Command Get-NetFirewallProfile -ErrorAction SilentlyContinue
    if ($getFirewall) {
        $enabled = Get-NetFirewallProfile -ErrorAction SilentlyContinue | Where-Object Enabled
        if ($enabled) { Write-Warning 'Windows防火墙已启用；脚本不会修改规则，请确认已允许 caddy.exe 的TCP 80/443入站。' }
    }
}
"@
    $path = Join-Path $RuntimeDir 'Caddyfile'
    Write-Utf8NoBom $path ($global + $site)
    return $path
}

function Start-Caddy($Deployment) {
    $caddy = Ensure-Caddy
    $caddyfile = Write-Caddyfile $Deployment
    $env:XDG_DATA_HOME = Join-Path $RuntimeDir 'caddy-data'
    $env:XDG_CONFIG_HOME = Join-Path $RuntimeDir 'caddy-config'
    & $caddy validate --config $caddyfile --adapter caddyfile
    if ($LASTEXITCODE -ne 0) { throw 'Caddyfile 验证失败。' }
    $p = Start-Process -FilePath $caddy -ArgumentList @('run','--config',('"{0}"' -f $caddyfile),'--adapter','caddyfile') -WorkingDirectory $RuntimeDir -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $LogsDir 'caddy.log') -RedirectStandardError (Join-Path $LogsDir 'caddy-error.log') -PassThru
    Set-Content -LiteralPath $CaddyPidFile -Value $p.Id -Encoding Ascii
    Write-Host '正在申请或加载 Let''s Encrypt 证书，最长等待 180 秒...'
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if (-not $curl) { throw '系统未找到 curl.exe，无法验证 HTTPS 证书。' }
    $deadline = (Get-Date).AddSeconds(180)
    do {
        Start-Sleep -Seconds 2
        if ($p.HasExited) { throw 'Caddy 已退出，请查看 logs\caddy-error.log。' }
        $target = "https://$($Deployment.domain)/"
        $resolve = "$($Deployment.domain):443:127.0.0.1"
        $code = (& $curl.Source -sS --connect-timeout 3 --resolve $resolve -o NUL -w '%{http_code}' $target 2>$null)
        if ($code -eq '404') { return $p }
    } while ((Get-Date) -lt $deadline)
    throw 'HTTPS 证书验证超时，请检查域名DNS、公网80/443、防火墙、端口转发和Caddy日志。'
}

function Start-All {
    Initialize-Config
    if ((Get-LivePid $ServerPidFile) -or (Get-LivePid $CaddyPidFile)) { Write-Host 'TT-Trigger 已在运行。'; return }
    $deployment = Load-Deployment
    New-Item -ItemType Directory -Force -Path $RuntimeDir,$LogsDir | Out-Null
    try {
        if ($deployment.mode -eq 'local_tailscale') {
            $ip = Find-TailscaleIPv4
            $listen = if ($ip) { "${ip}:8788" } else { $null }
            Start-Server $listen | Out-Null
            Write-Host 'TT-Trigger 已启动。'
            Write-Host 'Local API: http://127.0.0.1:8788/webhook'
            if ($ip) { Write-Host "Tailscale API: http://${ip}:8788/webhook" }
        } elseif ($deployment.mode -eq 'public_caddy') {
            Test-PublicPrerequisites $deployment
            Start-Server $null | Out-Null
            Start-Caddy $deployment | Out-Null
            Write-Host 'TT-Trigger 与 Caddy 已启动。'
            Write-Host "Public API: https://$($deployment.domain)/webhook"
        } else { throw 'deployment.json 中的 mode 无效。' }
        Write-Host "日志目录: $LogsDir"
    } catch {
        Stop-All
        throw
    }
}

function Show-Status {
    $serverPid = Get-LivePid $ServerPidFile
    $caddyPid = Get-LivePid $CaddyPidFile
    if ($serverPid) { Write-Host "TT-Trigger: running (PID $serverPid)" } else { Write-Host 'TT-Trigger: stopped' }
    if ($caddyPid) { Write-Host "Caddy: running (PID $caddyPid)" } else { Write-Host 'Caddy: stopped' }
    if (Test-Path $DeploymentPath) {
        $d = Load-Deployment
        Write-Host "Mode: $($d.mode)"
        if ($d.domain) { Write-Host "Domain: $($d.domain)" }
    }
}

function Manage-Keys {
    Initialize-Config
    $wasRunning = $null -ne (Get-LivePid $ServerPidFile)
    Write-Host '  1. 列出 keyId'
    Write-Host '  2. 新增 HMAC 密钥'
    Write-Host '  3. 吊销 HMAC 密钥'
    $choice = (Read-Host '输入 1、2 或 3').Trim()
    if ($choice -eq '1') { & $ServerExe --config $ConfigPath --key-list; return }
    if ($choice -notin @('2','3')) { throw '无效选择。' }
    if ($wasRunning) { Stop-All }
    if ($choice -eq '2') {
        $id = (Read-Host '新 keyId（字母、数字、点、下划线或连字符）').Trim()
        & $ServerExe --config $ConfigPath --key-add $id
    } else {
        & $ServerExe --config $ConfigPath --key-list
        $id = (Read-Host '要吊销的 keyId').Trim()
        & $ServerExe --config $ConfigPath --key-remove $id
    }
    if ($LASTEXITCODE -ne 0) { throw '密钥操作失败。' }
    Protect-Config
    if ($wasRunning) { Start-All }
}

try {
    switch ($Action) {
        'Start' { Start-All }
        'Stop' { Stop-All }
        'Status' { Show-Status }
        'Configure' { if ((Get-LivePid $ServerPidFile) -or (Get-LivePid $CaddyPidFile)) { Stop-All }; Configure-Mode }
        'Keys' { Manage-Keys }
    }
} catch {
    Write-Host "[ERROR] $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
