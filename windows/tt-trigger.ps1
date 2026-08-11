param(
    [ValidateSet('Start','Stop','Status','Configure','Keys')]
    [string]$Action = 'Start'
)

$ErrorActionPreference = 'Stop'
$HomeDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ServerExe = Join-Path $HomeDir 'tt-trigger-server.exe'
$ConfigPath = Join-Path $HomeDir 'config.json'
$LogsDir = Join-Path $HomeDir 'logs'
$ServerPidFile = Join-Path $HomeDir 'tt-trigger.pid'

function Get-LivePid {
    if (-not (Test-Path $ServerPidFile)) { return $null }
    $raw = (Get-Content $ServerPidFile -Raw).Trim()
    if ($raw -notmatch '^\d+$') { Remove-Item $ServerPidFile -Force; return $null }
    $process = Get-CimInstance Win32_Process -Filter "ProcessId = $raw" -ErrorAction SilentlyContinue
    if (-not $process) { Remove-Item $ServerPidFile -Force; return $null }
    $expected = [IO.Path]::GetFullPath($ServerExe)
    if (-not $process.ExecutablePath -or [IO.Path]::GetFullPath($process.ExecutablePath) -ne $expected) {
        Remove-Item $ServerPidFile -Force
        Write-Warning 'PID文件指向的不是TT-Trigger服务，已忽略。'
        return $null
    }
    return [int]$raw
}

function Protect-Config {
    try {
        $identity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
        foreach ($path in @($ConfigPath, "$ConfigPath.pre-3.0.bak", "$ConfigPath.pre-3.1.bak", "$ConfigPath.pre-4.0.bak")) {
            if (Test-Path $path) { & icacls.exe $path /inheritance:r /grant:r "${identity}:(F)" /grant:r 'SYSTEM:(F)' 2>$null | Out-Null }
        }
    } catch { Write-Warning '无法收紧 config.json ACL，请确认只有当前用户可以读取该文件。' }
}

function Protect-Runtime {
    try {
        $identity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
        foreach ($path in @($LogsDir, $ServerPidFile)) {
            if (Test-Path $path) { & icacls.exe $path /inheritance:r /grant:r "${identity}:(F)" /grant:r 'SYSTEM:(F)' 2>$null | Out-Null }
        }
    } catch { Write-Warning '无法收紧运行文件ACL。' }
}

function Initialize-Config {
    if (-not (Test-Path $ServerExe)) { throw "未找到 $ServerExe" }
    & $ServerExe --init --config $ConfigPath
    if ($LASTEXITCODE -ne 0) { throw '创建或迁移 config.json 失败。' }
    Protect-Config
}

function Find-TailscaleIPv4 {
    $candidates = @((Get-Command tailscale.exe -ErrorAction SilentlyContinue | ForEach-Object Source), "$env:ProgramFiles\Tailscale\tailscale.exe")
    $exe = $candidates | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
    if (-not $exe) { Write-Warning '未安装 Tailscale，仅监听 localhost。'; return $null }
    & $exe status *> $null
    if ($LASTEXITCODE -ne 0) { Write-Warning 'Tailscale 未连接，仅监听 localhost。'; return $null }
    $ip = [string](& $exe ip -4 2>$null | Select-Object -First 1)
    $ip = $ip.Trim()
    if ($ip -notmatch '^100\.(?:6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.(?:\d{1,3})\.(?:\d{1,3})$') {
        Write-Warning '无法获取有效的 Tailscale IPv4，仅监听 localhost。'; return $null
    }
    return $ip
}

function Start-All {
    Initialize-Config
    if (Get-LivePid) { Write-Host 'TT-Trigger 已在运行。'; return }
    New-Item -ItemType Directory -Force -Path $LogsDir | Out-Null
    $ip = Find-TailscaleIPv4
    $arguments = @('--config', ('"{0}"' -f $ConfigPath))
    if ($ip) { $arguments += @('--api-listen', "${ip}:8788") }
    $p = Start-Process -FilePath $ServerExe -ArgumentList $arguments -WorkingDirectory $HomeDir -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $LogsDir 'server.log') -RedirectStandardError (Join-Path $LogsDir 'server-error.log') -PassThru
    Set-Content -LiteralPath $ServerPidFile -Value $p.Id -Encoding Ascii
    Protect-Runtime
    Start-Sleep -Milliseconds 700
    if ($p.HasExited) { throw 'TT-Trigger 启动失败，请查看 logs\server-error.log。' }
    try { Invoke-RestMethod -Uri 'http://127.0.0.1:8788/health' -TimeoutSec 3 | Out-Null } catch { throw 'localhost 健康检查失败。' }
    Write-Host 'TT-Trigger 本地服务已启动。'
    Write-Host 'Local API: http://127.0.0.1:8788/webhook'
    if ($ip) { Write-Host "Tailscale API: http://${ip}:8788/webhook" }
    Write-Host '云端 E2EE 模式无需运行本程序，请直接在插件弹窗配置。'
}

function Stop-All {
    $processId = Get-LivePid
    if ($null -eq $processId) { Write-Host 'TT-Trigger 未运行。'; return }
    & taskkill.exe /PID $processId /T /F 2>$null | Out-Null
    Remove-Item $ServerPidFile -Force -ErrorAction SilentlyContinue
    Write-Host 'TT-Trigger stopped.'
}

function Show-Status {
    $processId = Get-LivePid
    if ($processId) { Write-Host "TT-Trigger: running (PID $processId)" } else { Write-Host 'TT-Trigger: stopped' }
    Write-Host 'Local API: http://127.0.0.1:8788/webhook'
    Write-Host 'Cloud E2EE: 请查看 Chrome 插件弹窗状态'
}

function Configure-Mode {
    Initialize-Config
    Write-Host '本地服务固定同时监听 localhost 和已连接的 Tailscale IPv4。'
    Write-Host '如需云端 E2EE，请在 Chrome 插件弹窗选择“云端 E2EE”。'
    Write-Host '旧 public_caddy 配置已自动备份并迁移，本程序不再启动或修改 Caddy。'
}

function Manage-Keys {
    Initialize-Config
    $wasRunning = $null -ne (Get-LivePid)
    Write-Host '  1. 列出 keyId'
    Write-Host '  2. 新增 HMAC 密钥'
    Write-Host '  3. 吊销 HMAC 密钥'
    Write-Host '  4. 轮换插件连接 Token'
    $choice = (Read-Host '输入 1、2、3 或 4').Trim()
    if ($choice -eq '1') { & $ServerExe --config $ConfigPath --key-list; return }
    if ($choice -notin @('2','3','4')) { throw '无效选择。' }
    if ($wasRunning) { Stop-All }
    if ($choice -eq '4') {
        & $ServerExe --config $ConfigPath --extension-token-rotate
    } elseif ($choice -eq '2') {
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
        'Configure' { Configure-Mode }
        'Keys' { Manage-Keys }
    }
} catch {
    Write-Host "[ERROR] $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
