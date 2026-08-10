@echo off
setlocal EnableExtensions
cd /d "%~dp0"

if not exist "tt-trigger.pid" (
  echo TT-Trigger is not running: tt-trigger.pid was not found.
  exit /b 0
)

set /p TT_PID=<"tt-trigger.pid"
if "%TT_PID%"=="" (
  echo [ERROR] tt-trigger.pid is empty.
  del /Q "tt-trigger.pid" >nul 2>&1
  exit /b 1
)

tasklist /FI "PID eq %TT_PID%" /NH 2>nul | findstr /R /C:"[ ]%TT_PID%[ ]" >nul
if errorlevel 1 (
  echo TT-Trigger process %TT_PID% no longer exists. Removing stale PID file.
  del /Q "tt-trigger.pid" >nul 2>&1
  exit /b 0
)

taskkill /PID %TT_PID% /T /F >nul
if errorlevel 1 (
  echo [ERROR] Could not stop TT-Trigger process %TT_PID%.
  exit /b 1
)

del /Q "tt-trigger.pid" >nul 2>&1
echo TT-Trigger stopped.
endlocal
