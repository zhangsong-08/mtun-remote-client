# Copyright (c) 2026 song08
# SPDX-License-Identifier: MIT

<#
.SYNOPSIS
  mtun 服务端一键安装脚本（Windows）

.DESCRIPTION
  从 GitHub Release 下载 mtun-windows-amd64.exe 或 arm64.exe，
  生成默认配置、设置管理密码，并通过 mtun 统一配置开机自启。
  网络访问 GitHub 困难时自动切换国内加速镜像，也支持离线安装。

  依赖：curl.exe（Win10/11 自带）。不依赖 Go。

.EXAMPLE
  # 管理员 PowerShell 执行（自动选源，配置开机自启）
  powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1

  # 国内服务器：指定镜像
  powershell -File .\install.ps1 -Mirror https://ghfast.top/

  # 离线安装（先在本机下载 exe 再传上去）
  powershell -File .\install.ps1 -LocalFile .\mtun-windows-amd64.exe

  # 指定目录
  powershell -File .\install.ps1 -InstallDir D:\mtun

  # 指定版本 + 自动模式（不交互设置密码，用 -AdminPassword 传入）
  powershell -File .\install.ps1 -Version v0.2.1 -AdminPassword MyStrongPwd123

  # 卸载
  powershell -File .\install.ps1 -Uninstall
#>

[CmdletBinding()]
param(
    [string]$Version = "",              # 留空 = 最新 release；离线安装时为 offline
    [string]$InstallDir = "",           # 留空 = C:\Program Files\mtun 或 %USERPROFILE%\mtun
    [string]$AdminPassword = "",        # 自动化安装时传入；为空时交互提示
    [string]$JoinPassword = "",         # 客户端接入密码；为空时保留已有值或自动生成
    [string]$Mirror = $env:MTUN_MIRROR, # GitHub 加速前缀，如 https://ghfast.top/
    [string]$LocalFile = "",            # 离线安装：使用本地 exe
    [switch]$UseNSSM = $true,           # 兼容旧命令行；自启方式由 mtun 统一管理
    [switch]$Uninstall = $false
)

$ErrorActionPreference = "Stop"
$repo = "zhangsong-08/mtun-remote-server"
$role = "server"
$service = "mtun-server"
$taskName = "mtun-server"

# 内置可用镜像（按顺序尝试，直连放最后）
$mirrorList = if ($Mirror) { @($Mirror) } else { @("https://ghfast.top/", "https://gh-proxy.com/", "") }

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $pr = New-Object Security.Principal.WindowsPrincipal($id)
    return $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-Utf8NoBom {
    param([string]$Path, [string]$Value)
    [System.IO.File]::WriteAllText($Path, $Value, [System.Text.UTF8Encoding]::new($false))
}

function Invoke-GitHubDownload {
    param([string]$Url, [string]$OutFile)
    foreach ($prefix in $mirrorList) {
        $target = "$prefix$Url"
        $sw = [Diagnostics.Stopwatch]::StartNew()
        curl.exe -fsSL --connect-timeout 10 --max-time 900 -o $OutFile $target 2>&1 | Out-Null
        if ($LASTEXITCODE -eq 0 -and (Test-Path $OutFile) -and (Get-Item $OutFile).Length -gt 0) {
            $sw.Stop()
            $size = (Get-Item $OutFile).Length
            $label = if ($prefix) { $prefix } else { "直连" }
            Write-Host "  下载完成($label)，$([math]::Round($size/1MB,2)) MB，耗时 $([math]::Round($sw.Elapsed.TotalSeconds,1)) 秒"
            return
        }
        $label = if ($prefix) { $prefix } else { "直连" }
        Write-Host "  源 $label 失败，切换下一个"
    }
    throw "下载失败：$Url`n国内服务器请指定镜像：-Mirror https://ghfast.top/`n或把 exe 传上来用 -LocalFile 离线安装"
}

function Stop-InstalledInstance {
    # 先停任务/服务，避免停止进程后被自动重新拉起。
    if (Get-Command Stop-ScheduledTask -ErrorAction SilentlyContinue) {
        Get-ScheduledTask -TaskName $taskName -TaskPath '\' -ErrorAction SilentlyContinue |
            Stop-ScheduledTask -ErrorAction SilentlyContinue
    }
    $svc = Get-Service -Name $service -ErrorAction SilentlyContinue
    if ($svc -and $svc.Status -ne "Stopped") {
        Stop-Service -Name $service -Force -ErrorAction SilentlyContinue
    }

    # 只终止 ExecutablePath 与当前安装路径完全一致的进程。
    $processes = @(Get-CimInstance Win32_Process -Filter "Name = 'mtun.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.ExecutablePath -and [string]::Equals($_.ExecutablePath, $exe, [StringComparison]::OrdinalIgnoreCase) })
    foreach ($p in $processes) {
        Stop-Process -Id $p.ProcessId -Force -ErrorAction Stop
        Wait-Process -Id $p.ProcessId -Timeout 10 -ErrorAction SilentlyContinue
    }
    if ($processes.Count -gt 0) { Start-Sleep -Milliseconds 500 }
}

function Remove-AutostartArtifacts {
    if (Get-Command nssm -ErrorAction SilentlyContinue) {
        & nssm stop $service 2>$null | Out-Null
        & nssm remove $service confirm 2>$null | Out-Null
    }
    if (Get-Service -Name $service -ErrorAction SilentlyContinue) {
        Stop-Service -Name $service -Force -ErrorAction SilentlyContinue
        & sc.exe delete $service 2>$null | Out-Null
    }

    if (Get-Command Unregister-ScheduledTask -ErrorAction SilentlyContinue) {
        Get-ScheduledTask -TaskName $taskName -TaskPath '\' -ErrorAction SilentlyContinue |
            Unregister-ScheduledTask -Confirm:$false -ErrorAction SilentlyContinue
    } else {
        & schtasks.exe /Delete /TN $taskName /F 2>$null | Out-Null
    }

    $startupDir = [Environment]::GetFolderPath("Startup")
    Remove-Item (Join-Path $startupDir "mtun-server.lnk") -Force -ErrorAction SilentlyContinue
}

function Enable-Autostart {
    $output = & $exe __autostart enable -role $role -c $cfg -start
    $exitCode = $LASTEXITCODE
    $json = ($output -join [Environment]::NewLine).Trim()
    if ($exitCode -ne 0) {
        throw "配置开机自启失败（退出码 $exitCode）：$json"
    }
    try {
        $result = $json | ConvertFrom-Json
    } catch {
        throw "mtun __autostart enable 未返回有效 JSON：$json"
    }
    if (-not $result.supported -or -not $result.enabled) {
        throw "开机自启未启用：$json"
    }
    if ($result.message) { Write-Host "  $($result.message)" }
    Write-Host "[OK] mtun 已启动并配置开机自启（$($result.mode)）"
}

# ---------- 参数 ----------
if (-not $InstallDir) {
    if (Test-Admin) { $InstallDir = "C:\Program Files\mtun" }
    else            { $InstallDir = "$env:USERPROFILE\mtun" }
}
$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
$binDir = Join-Path $InstallDir "bin"
$cfgDir = Join-Path $InstallDir "configs"
$logDir = Join-Path $InstallDir "logs"
$cfg = Join-Path $cfgDir "server.yaml"
$exe = Join-Path $binDir "mtun.exe"

Write-Host "======================================================"
Write-Host " mtun 服务端安装"
Write-Host "======================================================"
Write-Host " 安装目录:   $InstallDir"
Write-Host " 管理员:     $(Test-Admin)"
Write-Host ""

# ---------- 卸载 ----------
if ($Uninstall) {
    Write-Host "==> 卸载 mtun"
    Stop-InstalledInstance
    if (Test-Path $exe) {
        $removeOutput = & $exe __autostart remove -role $role -c $cfg 2>&1
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "mtun __autostart remove 失败，将继续清理旧自启动项：$($removeOutput -join ' ')"
        }
    }
    Remove-AutostartArtifacts
    Remove-Item $InstallDir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host "已卸载 mtun：$InstallDir"
    exit 0
}

# ---------- 版本 ----------
if (-not $Version -and $LocalFile) { $Version = "offline" }
if (-not $Version) {
    Write-Host "==> 查询最新 release"
    foreach ($prefix in $mirrorList) {
        $raw = curl.exe -fsSL --connect-timeout 8 --max-time 25 "${prefix}https://api.github.com/repos/$repo/releases/latest" 2>$null
        if ($raw) {
            try {
                $Version = ($raw | ConvertFrom-Json).tag_name
                if ($Version) { break }
            } catch { }
        }
        $loc = curl.exe -fsSI --connect-timeout 8 --max-time 25 "${prefix}https://github.com/$repo/releases/latest" 2>$null |
            Select-String -Pattern "^location:" -CaseSensitive:$false | Select-Object -First 1
        if ($loc) {
            $Version = ($loc.ToString().Trim() -split "/")[-1]
            if ($Version) { break }
        }
    }
    if (-not $Version) {
        throw "无法获取最新版本（网络不通）。请用 -Version 指定，或使用 -LocalFile 离线安装"
    }
}

# ---------- 架构 ----------
$archText = "$env:PROCESSOR_ARCHITECTURE $env:PROCESSOR_ARCHITEW6432 $((Get-CimInstance Win32_OperatingSystem).OSArchitecture)"
$tag = if ($archText -match "ARM64") { "mtun-windows-arm64.exe" } else { "mtun-windows-amd64.exe" }
Write-Host "==> 使用版本 $Version，资产 $tag"

# ---------- 下载与校验 ----------
New-Item -ItemType Directory -Force -Path $binDir,$cfgDir,$logDir | Out-Null
$url = "https://github.com/$repo/releases/download/$Version/$tag"
$sum = "https://github.com/$repo/releases/download/$Version/checksums.txt"
$tmp = Join-Path $env:TEMP ("mtun-install-" + [Guid]::NewGuid().ToString("N") + ".exe")

if ($LocalFile) {
    Write-Host "==> 离线安装，使用本地文件 $LocalFile"
    if (-not (Test-Path $LocalFile)) { throw "文件不存在: $LocalFile" }
    Copy-Item $LocalFile $tmp -Force
} else {
    Write-Host "==> 下载 $url"
    Invoke-GitHubDownload -Url $url -OutFile $tmp

    $sumTmp = Join-Path $env:TEMP ("mtun-checksums-" + [Guid]::NewGuid().ToString("N") + ".txt")
    Invoke-GitHubDownload -Url $sum -OutFile $sumTmp
    $line = Get-Content $sumTmp | Where-Object {
        $parts = ($_.Trim() -split '\s+')
        $parts.Count -ge 2 -and ($parts[1] -eq $tag -or $parts[1] -eq "*$tag")
    } | Select-Object -First 1
    $want = if ($line) { ($line.Trim() -split '\s+')[0].ToLower() } else { "" }
    $local = (Get-FileHash $tmp -Algorithm SHA256).Hash.ToLower()
    Remove-Item $sumTmp -Force -ErrorAction SilentlyContinue
    if (-not $want) { throw "checksum 清单中没有 $tag，拒绝安装未校验的二进制" }
    if ($want -ne $local) {
        Remove-Item $tmp -Force -ErrorAction SilentlyContinue
        throw "checksum 不匹配（期望 $want，实际 $local），已拒绝安装"
    }
    Write-Host "==> checksum 校验通过"
}

# 替换二进制前停止当前安装实例，避免文件占用。
Stop-InstalledInstance
Move-Item $tmp $exe -Force
& $exe version
if ($LASTEXITCODE -ne 0) { throw "mtun 二进制无法执行" }

# ---------- 生成配置 ----------
if (-not (Test-Path $cfg)) {
    Write-Host "==> 生成默认配置"
    $defaultYaml = & $exe default server | Out-String
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($defaultYaml)) {
        $defaultYaml = @'
role: server
control: ":10000"
wg: ":10001"
relay: ":10002"
stun: ":10003"
dashboard: "127.0.0.1:10004"
update_repo: "zhangsong-08/mtun-remote-server"
overlay:
  cidr: "100.64.0.0/10"
  server_ip: "100.64.0.1"
publish:
  port_range: "10100-10999"
'@
    }
    Write-Utf8NoBom -Path $cfg -Value $defaultYaml
}

& $exe check -c $cfg -role server | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host "配置文件校验失败，详情：" -ForegroundColor Red
    & $exe check -c $cfg -role server
    throw "请修正 $cfg 后重试"
}

# ---------- 接入密码 ----------
$join = "$cfg.join"
if (-not $JoinPassword -and (Test-Path $join)) {
    $JoinPassword = (Get-Content $join -Raw).Trim()
}
if (-not $JoinPassword) {
    $bytes = New-Object byte[] 18
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes) | Out-Null
    $JoinPassword = -join ($bytes | ForEach-Object { $_.ToString("x2") })
}
& $exe access -c $cfg -password $JoinPassword | Out-Null
if ($LASTEXITCODE -ne 0) { throw "写入客户端接入密码失败" }

# ---------- 管理密码 ----------
$passFile = "$cfg.passwd"
if (Test-Path $passFile) {
    Write-Host "==> 管理密码已存在，跳过"
} else {
    Write-Host "==> 设置管理密码（至少 8 位）"
    if ($AdminPassword) {
        Write-Host "  使用 -AdminPassword 指定的密码"
        & $exe passwd -c $cfg -password $AdminPassword | Out-Null
    } else {
        & $exe passwd -c $cfg
    }
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path $passFile)) {
        throw "管理密码未设置，安装已终止"
    }
}

# ---------- 统一开机自启与启动 ----------
Write-Host "==> 配置开机自启并启动"
Enable-Autostart

Write-Host ""
Write-Host "======================================================"
Write-Host " 安装完成"
Write-Host "======================================================"
Write-Host "  控制台:         http://127.0.0.1:10004"
Write-Host "  控制面:         :10000 (TLS 1.3)"
Write-Host "  转发范围:       10100-10999"
Write-Host "  配置:           $cfg"
Write-Host "  日志:           $logDir"
Write-Host "  接入密码:       $JoinPassword"
Write-Host ""
Write-Host "  在线更新:       & `"$exe`" update -c `"$cfg`""
Write-Host "  自启动状态:     & `"$exe`" __autostart status -role server -c `"$cfg`""
Write-Host "  卸载:           powershell -File install.ps1 -Uninstall"
Write-Host ""
Write-Host "  [!]  如果是公网服务器，请放行防火墙端口：10000/TCP、10100-10999/TCP"
Write-Host "  [!]  别忘了把接入密码填给客户端"
