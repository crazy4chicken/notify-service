@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"

set "GO=go"
where go >nul 2>nul
if errorlevel 1 set "GO=D:\tools\go\bin\go.exe"

if not exist notify-service.exe (
  echo [构建] %GO% build
  "%GO%" build -o notify-service.exe .
  if errorlevel 1 (
    echo 构建失败：确认 Go 已安装，或修改本脚本中的 GO 路径。
    exit /b 1
  )
)

echo [启动] 控制台 http://127.0.0.1:8090/    按 Ctrl+C 退出
notify-service.exe %*
