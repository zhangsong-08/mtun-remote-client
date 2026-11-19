# Copyright (c) 2026 song08
# SPDX-License-Identifier: MIT

[CmdletBinding()]
param(
    [string]$Config = "configs/server.example.yaml",
    [string]$Token = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$exe = Join-Path $root "bin\mtun.exe"
if (-not (Test-Path $exe)) {
    Write-Host "未找到 bin\mtun.exe，正在编译..."
    go build -trimpath -o "bin\mtun.exe" ".\cmd\mtun"
    if ($LASTEXITCODE -ne 0) { throw "编译失败" }
}

# 1. 客户端接入密码：-Token > MTUN_JOIN_TOKEN > configs\join.token > 自动生成
if (-not $Token) { $Token = $env:MTUN_JOIN_TOKEN }
$tokenFile = Join-Path $root "configs\join.token"
if (-not $Token) {
    if (Test-Path $tokenFile) {
        $Token = (Get-Content $tokenFile -Raw).Trim()
    } else {
        $bytes = New-Object byte[] 18
        $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
        $rng.GetBytes($bytes)
        $rng.Dispose()
        $Token = -join ($bytes | ForEach-Object { $_.ToString("x2") })
        New-Item -ItemType Directory -Force (Split-Path $tokenFile) | Out-Null
        Set-Content -Path $tokenFile -Value $Token -NoNewline
        Write-Host "已生成客户端接入密码: configs\join.token"
    }
}
if ($Token.Length -lt 8) { throw "客户端接入密码至少 8 位" }
$env:MTUN_JOIN_TOKEN = $Token

# 2. 把接入密码写入服务端配置旁的 <配置>.join（终端设置）
$joinFile = "$Config.join"
$currentJoin = ""
if (Test-Path $joinFile) { $currentJoin = (Get-Content $joinFile -Raw).Trim() }
if ($currentJoin -ne $Token) {
    & $exe access -c $Config -password $Token
    if ($LASTEXITCODE -ne 0) { throw "写入客户端接入密码失败" }
}

# 3. 服务端管理密码必须由管理员在终端设置
if (-not (Test-Path "$Config.passwd")) {
    Write-Host ""
    Write-Host "首次启动需要设置服务端管理密码（Web 控制台登录用，至少 8 位）："
    & $exe passwd -c $Config
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path "$Config.passwd")) {
        throw "服务端管理密码未设置，已终止启动"
    }
}

Write-Host ""
Write-Host "服务端启动中"
Write-Host "  配置文件:       $Config"
Write-Host "  控制台:         http://127.0.0.1:10004"
Write-Host "  管理密码文件:   $Config.passwd（由终端 mtun passwd 设置）"
Write-Host "  客户端接入密码: configs\join.token（客户端连接时填写）"
Write-Host ""

& $exe server -c $Config
exit $LASTEXITCODE
