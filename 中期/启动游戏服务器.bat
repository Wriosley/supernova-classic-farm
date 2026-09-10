@echo off
setlocal
chcp 65001 >nul
REM 中期演示入口：转到旁边的仓库再启动。不要只拷这个 bat，仓库也要在。
cd /d "%~dp0..\supernova-classic-farm"
if not exist "start-servers.ps1" (
  echo [错误] 找不到仓库 D:\课设\supernova-classic-farm
  echo 请把本文件和 supernova-classic-farm 放在同一个「课设」目录下。
  pause
  exit /b 1
)
call "启动游戏服务器.bat"
