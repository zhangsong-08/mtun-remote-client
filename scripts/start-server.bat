@echo off
rem Copyright (c) 2026 song08
rem SPDX-License-Identifier: MIT
rem 服务端快捷启动：双击或在终端执行，可附加参数，如 -Config configs\server.yaml
cd /d "%~dp0.."
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-server.ps1" %*
pause
