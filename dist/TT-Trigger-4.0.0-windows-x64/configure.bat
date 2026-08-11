@echo off
cd /d "%~dp0"
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0tt-trigger.ps1" Configure
if errorlevel 1 pause
