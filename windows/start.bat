@echo off
setlocal EnableExtensions
cd /d "%~dp0"
set "TT_HOME=%~dp0"

if not exist "tt-trigger-server.exe" (
  echo [ERROR] tt-trigger-server.exe was not found in %CD%
  pause
  exit /b 1
)

set "FIRST_RUN=0"
if not exist "config.json" (
  set "FIRST_RUN=1"
  echo Creating config.json...
  "tt-trigger-server.exe" --init --config "config.json"
  if errorlevel 1 (
    echo [ERROR] Could not create config.json.
    pause
    exit /b 1
  )
)

if exist "tt-trigger.pid" (
  set /p OLD_PID=<"tt-trigger.pid"
  tasklist /FI "PID eq %OLD_PID%" /NH 2>nul | findstr /R /C:"[ ]%OLD_PID%[ ]" >nul
  if not errorlevel 1 (
    echo TT-Trigger is already running. PID: %OLD_PID%
    if "%FIRST_RUN%"=="1" pause
    exit /b 0
  )
  del /Q "tt-trigger.pid" >nul 2>&1
)

if not exist "logs" mkdir "logs"

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -Command ^
  "$homeDir=$env:TT_HOME; $exe=Join-Path $homeDir 'tt-trigger-server.exe'; $stdout=Join-Path $homeDir 'logs\server.log'; $stderr=Join-Path $homeDir 'logs\server-error.log'; $pidFile=Join-Path $homeDir 'tt-trigger.pid'; $p=Start-Process -FilePath $exe -WorkingDirectory $homeDir -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru; Set-Content -LiteralPath $pidFile -Value $p.Id -Encoding ascii; Start-Sleep -Milliseconds 700; if ($p.HasExited) { exit 1 }"

if errorlevel 1 (
  echo [ERROR] TT-Trigger failed to start. See logs\server-error.log.
  if exist "tt-trigger.pid" del /Q "tt-trigger.pid" >nul 2>&1
  pause
  exit /b 1
)

set /p TT_PID=<"tt-trigger.pid"
echo TT-Trigger started. PID: %TT_PID%
echo URL:      http://YOUR_PUBLIC_IP:8787/trigger?token=YOUR_TOKEN^&symbol=BTC
echo Health:  http://127.0.0.1:8787/health
echo Logs:    %CD%\logs

if "%FIRST_RUN%"=="1" (
  echo.
  echo Copy the token shown above into the Chrome extension settings.
  echo The token is also stored in config.json.
  pause
)

endlocal
