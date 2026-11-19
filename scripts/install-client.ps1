# Copyright (c) 2026 song08
# SPDX-License-Identifier: MIT

<#
.SYNOPSIS
  mtun 客户端一键安装脚本（Windows）

.DESCRIPTION
  从 mtun-remote-client Release 下载并安装客户端，保留已有配置，
  设置服务端、节点和面板参数，并通过 mtun 统一配置开机自启。

.EXAMPLE
  powershell -File .\install-client.ps1 -Server 1.2.3.4:10000 -Key 接入密码

.EXAMPLE
  powershell -File .\install-client.ps1 -LocalFile .\mtun-windows-amd64.exe -Server 1.2.3.4:10000 -Key 接入密码

.EXAMPLE
  powershell -File .\install-client.ps1 -Uninstall
#>

[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$InstallDir = "",
    [string]$Server = "",
    [string]$Key = $env:MTUN_JOIN_TOKEN,
    [string]$Node = "",
    [string]$Panel = "",
    [string]$AdminPassword = $env:MTUN_ADMIN_PASSWORD,
    [string]$Mirror = $env:MTUN_MIRROR,
    [string]$LocalFile = "",
    [switch]$Uninstall = $false
)

$ErrorActionPreference = "Stop"
$repo = "zhangsong-08/mtun-remote-client"
$role = "client"
$service = "mtun-client"
$taskName = "mtun-client"
$panelSpecified = $PSBoundParameters.ContainsKey("Panel") -or -not [string]::IsNullOrEmpty($env:MTUN_PANEL)
if (-not $Panel) { $Panel = if ($env:MTUN_PANEL) { $env:MTUN_PANEL } else { "127.0.0.1:10005" } }
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
    throw "下载失败：$Url`n可指定 -Mirror https://ghfast.top/，或使用 -LocalFile 离线安装"
}

function Stop-InstalledInstance {
    if (Get-Command Stop-ScheduledTask -ErrorAction SilentlyContinue) {
        Get-ScheduledTask -TaskName $taskName -TaskPath '\' -ErrorAction SilentlyContinue |
            Stop-ScheduledTask -ErrorAction SilentlyContinue
    }
    $svc = Get-Service -Name $service -ErrorAction SilentlyContinue
    if ($svc -and $svc.Status -ne "Stopped") {
        Stop-Service -Name $service -Force -ErrorAction SilentlyContinue
    }

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
    Remove-Item (Join-Path $startupDir "mtun-client.lnk") -Force -ErrorAction SilentlyContinue
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
    Write-Host "[OK] mtun 客户端已启动并配置开机自启（$($result.mode)）"
}

function Set-YamlScalar {
    param([string]$Text, [string]$Name, [string]$Value)
    $encoded = $Value | ConvertTo-Json -Compress
    $replacement = ("${Name}: $encoded").Replace('$', '$$')
    $pattern = "(?m)^$([regex]::Escape($Name))\s*:[^\r\n]*"
    if ([regex]::IsMatch($Text, $pattern)) {
        return [regex]::Replace($Text, $pattern, $replacement)
    }
    $separator = if ($Text.Length -eq 0 -or $Text.EndsWith("`n")) { "" } else { [Environment]::NewLine }
    return $Text + $separator + "${Name}: $encoded" + [Environment]::NewLine
}

function Get-YamlScalar {
    param([string]$Text, [string]$Name)
    $match = [regex]::Match($Text, "(?m)^$([regex]::Escape($Name))\s*:[ \t]*(.*?)[ \t]*\r?$")
    if (-not $match.Success) { return "" }
    $value = $match.Groups[1].Value.Trim()
    if ($value -match '^"((?:\\.|[^"\\])*)"(?:\s+#.*)?$') {
        return ('"' + $Matches[1] + '"') | ConvertFrom-Json
    }
    if ($value -match "^'((?:[^']|'')*)'(?:\s+#.*)?$") {
        return $Matches[1].Replace("''", "'")
    }
    return [regex]::Replace($value, '\s+#.*$', '').Trim()
}

# ---------- 参数 ----------
if (-not $InstallDir) {
    if (Test-Admin) { $InstallDir = "C:\Program Files\mtun-client" }
    else            { $InstallDir = "$env:USERPROFILE\mtun-client" }
}
$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
$binDir = Join-Path $InstallDir "bin"
$cfgDir = Join-Path $InstallDir "configs"
$logDir = Join-Path $InstallDir "logs"
$cfg = Join-Path $cfgDir "client.yaml"
$join = "$cfg.join"
$exe = Join-Path $binDir "mtun.exe"

Write-Host "======================================================"
Write-Host " mtun 客户端安装"
Write-Host "======================================================"
Write-Host " 安装目录:   $InstallDir"
Write-Host " 管理员:     $(Test-Admin)"
Write-Host ""

# ---------- 卸载 ----------
if ($Uninstall) {
    Write-Host "==> 卸载 mtun 客户端"
    Stop-InstalledInstance
    if (Test-Path $exe) {
        $removeOutput = & $exe __autostart remove -role $role -c $cfg 2>&1
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "mtun __autostart remove 失败，将继续清理旧自启动项：$($removeOutput -join ' ')"
        }
    }
    Remove-AutostartArtifacts
    Remove-Item $InstallDir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host "已卸载 mtun 客户端：$InstallDir"
    exit 0
}

$existingServer = ""
if (Test-Path $cfg) {
    $existingServer = Get-YamlScalar -Text ([System.IO.File]::ReadAllText($cfg)) -Name "server"
}
if (-not (Test-Path $cfg) -and -not $Server) {
    throw "首次安装必须提供 -Server，例如：-Server 1.2.3.4:10000"
}
if (-not (Test-Path $join) -and -not $Key) {
    throw "首次安装必须提供 -Key，或设置 MTUN_JOIN_TOKEN"
}
if (-not $Server -and $Key -and -not $existingServer) {
    throw "已有配置中没有 server，更新接入密码时请同时提供 -Server"
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
        throw "无法获取最新版本。请用 -Version 指定，或使用 -LocalFile 离线安装"
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
$tmp = Join-Path $env:TEMP ("mtun-client-install-" + [Guid]::NewGuid().ToString("N") + ".exe")

if ($LocalFile) {
    Write-Host "==> 离线安装，使用本地文件 $LocalFile"
    if (-not (Test-Path $LocalFile)) { throw "文件不存在: $LocalFile" }
    Copy-Item $LocalFile $tmp -Force
} else {
    Write-Host "==> 下载 $url"
    Invoke-GitHubDownload -Url $url -OutFile $tmp

    $sumTmp = Join-Path $env:TEMP ("mtun-client-checksums-" + [Guid]::NewGuid().ToString("N") + ".txt")
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

# 替换二进制前停止当前安装实例，后续 connect 也只进行离线配置。
Stop-InstalledInstance
Move-Item $tmp $exe -Force
& $exe version
if ($LASTEXITCODE -ne 0) { throw "mtun 二进制无法执行" }

# ---------- 配置 ----------
$newConfig = -not (Test-Path $cfg)
if ($newConfig) {
    Write-Host "==> 生成客户端配置"
    $defaultYaml = & $exe default client | Out-String
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($defaultYaml)) {
        $defaultYaml = @'
role: client
node: ""
server: ""
control: ":10000"
wg: ":10001"
relay: ":10002"
stun: ":10003"
dashboard: "127.0.0.1:10005"
update_repo: "zhangsong-08/mtun-remote-client"
overlay:
  cidr: "100.64.0.0/10"
  server_ip: "100.64.0.1"
publish:
  port_range: "10100-10999"
  rules: []
routes: []
acl: []
'@
    }
    Write-Utf8NoBom -Path $cfg -Value $defaultYaml
}

$yaml = [System.IO.File]::ReadAllText($cfg)
$yaml = Set-YamlScalar -Text $yaml -Name "update_repo" -Value $repo
if ($Node) { $yaml = Set-YamlScalar -Text $yaml -Name "node" -Value $Node }
if ($newConfig -or $panelSpecified) {
    $yaml = Set-YamlScalar -Text $yaml -Name "dashboard" -Value $Panel
}
Write-Utf8NoBom -Path $cfg -Value $yaml

$connectServer = if ($Server) { $Server } else { $existingServer }
if ($Server -or $Key) {
    $connectArgs = @("connect", "-server", $connectServer)
    if ($Key) { $connectArgs += @("-key", $Key) }
    $connectArgs += @("-c", $cfg)
    & $exe @connectArgs
    if ($LASTEXITCODE -ne 0) { throw "写入客户端服务端地址或接入密码失败" }
}

& $exe check -c $cfg -role client | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host "配置文件校验失败，详情：" -ForegroundColor Red
    & $exe check -c $cfg -role client
    throw "请修正 $cfg 后重试"
}

# ---------- 管理密码 ----------
$passFile = "$cfg.passwd"
if (Test-Path $passFile) {
    Write-Host "==> 管理密码已存在，跳过"
} else {
    Write-Host "==> 设置客户端管理密码（至少 8 位）"
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

$finalYaml = [System.IO.File]::ReadAllText($cfg)
$finalServer = Get-YamlScalar -Text $finalYaml -Name "server"
$finalPanel = Get-YamlScalar -Text $finalYaml -Name "dashboard"
if (-not $finalPanel) { $finalPanel = $Panel }
Write-Host ""
Write-Host "======================================================"
Write-Host " 客户端安装完成"
Write-Host "======================================================"
Write-Host "  控制台:         http://$finalPanel"
Write-Host "  服务端:         $finalServer"
Write-Host "  配置:           $cfg"
Write-Host "  日志:           $logDir"
Write-Host ""
Write-Host "  在线更新:       & `"$exe`" update -c `"$cfg`""
Write-Host "  自启动状态:     & `"$exe`" __autostart status -role client -c `"$cfg`""
Write-Host "  卸载:           powershell -File install-client.ps1 -Uninstall"
