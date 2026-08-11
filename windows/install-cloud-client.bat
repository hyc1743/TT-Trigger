@echo off
setlocal
python -m pip install "cryptography>=43,<47"
if errorlevel 1 exit /b 1
echo Cloud E2EE Python dependency installed.
