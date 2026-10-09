@echo off
rem double click this it just runs install\fengard-setup.ps1
setlocal
set "PS1=%~dp0install\fengard-setup.ps1"
if not exist "%PS1%" set "PS1=%~dp0fengard-setup.ps1"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%PS1%" %*
exit /b %ERRORLEVEL%
