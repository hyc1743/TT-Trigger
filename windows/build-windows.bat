@echo off
setlocal EnableExtensions
cd /d "%~dp0\.."

where go >nul 2>&1
if errorlevel 1 (
  echo [ERROR] Go was not found. Install Go 1.22 or newer first.
  exit /b 1
)

if not exist "windows" mkdir "windows"
set "CGO_ENABLED=0"
set "GOOS=windows"
set "GOARCH=amd64"
go build -trimpath -ldflags "-s -w" -o "windows\tt-trigger-server.exe" ".\cmd\tt-trigger-server"
if errorlevel 1 exit /b 1

echo Built windows\tt-trigger-server.exe
endlocal
