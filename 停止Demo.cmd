@echo off
setlocal
"%~dp0teacher-demo\python\python.exe" "%~dp0teacher-demo\portable\launcher.py" stop %*
if errorlevel 1 pause
endlocal
