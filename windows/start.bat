@echo off
setlocal EnableExtensions
cd /d "%~dp0"
set "TT_HOME=%~dp0"

if not exist "tt-trigger-server.exe" (
  echo [ERROR] tt-trigger-server.exe was not found in %CD%
  pause
  exit /b 1
)

set "TAILSCALE_EXE="
for /f "delims=" %%I in ('where tailscale.exe 2^>nul') do if not defined TAILSCALE_EXE set "TAILSCALE_EXE=%%I"
if not defined TAILSCALE_EXE if exist "%ProgramFiles%\Tailscale\tailscale.exe" set "TAILSCALE_EXE=%ProgramFiles%\Tailscale\tailscale.exe"
if not defined TAILSCALE_EXE (
  echo [ERROR] Tailscale was not found. Install and sign in to Tailscale first.
  pause
  exit /b 1
)

"%TAILSCALE_EXE%" status >nul 2>&1
if errorlevel 1 (
  echo [ERROR] Tailscale is not connected. Open Tailscale and sign in first.
  pause
  exit /b 1
)

for %%I in ("%TAILSCALE_EXE%") do set "PATH=%%~dpI;%PATH%"
set "TAILSCALE_IP="
for /f "delims=" %%I in ('tailscale.exe ip -4 2^>nul') do if not defined TAILSCALE_IP set "TAILSCALE_IP=%%I"
if not defined TAILSCALE_IP (
  echo [ERROR] Could not read the local Tailscale IPv4 address.
  pause
  exit /b 1
)
set "TT_API_LISTEN=%TAILSCALE_IP%:8788"

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
  "$homeDir=$env:TT_HOME; $exe=Join-Path $homeDir 'tt-trigger-server.exe'; $stdout=Join-Path $homeDir 'logs\server.log'; $stderr=Join-Path $homeDir 'logs\server-error.log'; $pidFile=Join-Path $homeDir 'tt-trigger.pid'; $arguments=@('--api-listen',$env:TT_API_LISTEN); $p=Start-Process -FilePath $exe -ArgumentList $arguments -WorkingDirectory $homeDir -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru; Set-Content -LiteralPath $pidFile -Value $p.Id -Encoding ascii; Start-Sleep -Milliseconds 700; if ($p.HasExited) { exit 1 }"

if errorlevel 1 (
  echo [ERROR] TT-Trigger failed to start. See logs\server-error.log.
  if exist "tt-trigger.pid" del /Q "tt-trigger.pid" >nul 2>&1
  pause
  exit /b 1
)

set /p TT_PID=<"tt-trigger.pid"

echo TT-Trigger started. PID: %TT_PID%
echo Local extension relay: ws://127.0.0.1:8787/extension
echo Tailscale API: http://%TAILSCALE_IP%:8788
echo URL example: http://%TAILSCALE_IP%:8788/trigger?token=YOUR_TOKEN^&symbol=BTC
echo Logs:    %CD%\logs

if "%FIRST_RUN%"=="1" (
  echo.
  echo Copy the token shown above into the Chrome extension settings.
  echo The token is also stored in config.json.
  pause
)

endlocal
