@echo off
rem Copyright (c) 2026 song08
rem SPDX-License-Identifier: MIT
rem 客户端快捷启动：双击运行，或在终端传参：
rem   scripts\start-client.bat -Config configs\client.yaml -Server 1.2.3.4:10000
cd /d "%~dp0.."
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-client.ps1" %*
pause
