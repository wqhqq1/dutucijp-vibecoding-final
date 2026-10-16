@echo off
setlocal
powershell.exe -NoProfile -File "%~dp0tools\launch-demo.ps1"
if errorlevel 1 pause
endlocal
