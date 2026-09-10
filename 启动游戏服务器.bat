@echo off
setlocal
cd /d "%~dp0"
chcp 65001 >nul

if exist "%ProgramFiles(x86)%\Go\bin\go.exe" set "PATH=%ProgramFiles(x86)%\Go\bin;%PATH%"
if exist "%ProgramFiles%\Go\bin\go.exe" set "PATH=%ProgramFiles%\Go\bin;%PATH%"

where go >nul 2>&1
if errorlevel 1 (
  echo [错误] 找不到 Go。请先安装 Go，或把 go.exe 所在目录加入 PATH。
  pause
  exit /b 1
)

echo 正在启动游戏服务器  http://127.0.0.1:8080
echo 有 .env 则用 MySQL，否则用内存模式（关窗口后数据会丢失）。
echo 不要关掉这个黑窗口；停止请按 Ctrl+C。
echo.

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-servers.ps1"
set "ERR=%ERRORLEVEL%"
echo.
if not "%ERR%"=="0" (
  echo 服务器退出，错误码 %ERR%。
) else (
  echo 服务器已停止。
)
pause
exit /b %ERR%
