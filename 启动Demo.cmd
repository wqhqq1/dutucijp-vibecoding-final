@echo off
setlocal
if not exist "%~dp0teacher-demo\python\python.exe" (
  echo The independent teacher-demo folder is missing. Use the full teacher delivery ZIP.
  pause
  exit /b 1
)
"%~dp0teacher-demo\python\python.exe" "%~dp0teacher-demo\portable\launcher.py" start %*
if errorlevel 1 pause
endlocal
