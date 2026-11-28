# Copyright (c) 2026 song08
# SPDX-License-Identifier: MIT

<#
.SYNOPSIS
  MTUN 客户端安装脚本（Windows）

.DESCRIPTION
  从 mtun-remote-client Release 下载并安装客户端，保留已有配置，
  设置服务端地址、节点名称、面板参数与接入密码，并通过 mtun 统一配置开机自启。

  安装过程中 Ctrl+C 会被忽略，避免把系统停在半安装状态。

  依赖：curl.exe（Win10/11 自带）。不依赖 Go。

.NOTES
  Copyright (c) 2026 song08 · SPDX-License-Identifier: MIT
  最后更新: 2026-11-28
  脚本版本: 2026.11.28

.EXAMPLE
  powershell -File .\install-client.ps1 -Server 1.2.3.4:10000 -Key 接入密码 -Node office-pc

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
    [switch]$Uninstall = $false,
    [switch]$Help = $false
)

$ErrorActionPreference = "Stop"

# ---------- 元信息 ----------
$AppName     = "MTUN"
$RoleName    = "客户端"
$Copyright   = "Copyright (c) 2026 song08 · SPDX-License-Identifier: MIT"
$LastUpdated = "2026-11-28"
$ScriptRev   = "2026.11.28"
$repo        = "zhangsong-08/mtun-remote-client"
$role        = "client"
$service     = "mtun-client"
$taskName    = "mtun-client"
$totalSteps  = 8
$script:stepIndex = 0

# ---------- 输出样式 ----------
function Write-Banner {
    Write-Host ""
    Write-Host "================================================================" -ForegroundColor Cyan
    Write-Host "  __  __ _____ _   _ _   _" -ForegroundColor Cyan
    Write-Host " |  \/  |_   _| | | | \ | |" -ForegroundColor Cyan
    Write-Host " | |\/| | | | | | | |  \| |" -ForegroundColor Cyan
    Write-Host " | |  | | | | | |_| | |\  |" -ForegroundColor Cyan
    Write-Host " |_|  |_| |_|  \___/|_| \_|" -ForegroundColor Cyan
    Write-Host "================================================================" -ForegroundColor Cyan
    Write-Host "  $AppName 内网穿透 · $RoleName 安装脚本" -ForegroundColor White
    Write-Host "  $Copyright" -ForegroundColor DarkGray
    Write-Host "  最后更新: $LastUpdated    脚本版本: $ScriptRev"
    Write-Host "================================================================" -ForegroundColor Cyan
}

function Write-Step([string]$Text) {
    $script:stepIndex++
    Write-Host ""
    Write-Host ("[{0}] [{1}/{2}] {3}" -f (Get-Date -Format 'HH:mm:ss'), $script:stepIndex, $totalSteps, $Text) -ForegroundColor Cyan
}
function Write-Info([string]$Text)   { Write-Host "        · $Text" }
function Write-Detail([string]$Text) { Write-Host "        $Text" -ForegroundColor DarkGray }
function Write-Ok([string]$Text)     { Write-Host "        [OK] $Text" -ForegroundColor Green }
function Write-WarnLine([string]$Text) { Write-Host "        [!] $Text" -ForegroundColor Yellow }
function Write-Fail([string]$Text)   { Write-Host "        [失败] $Text" -ForegroundColor Red }

function Format-Size([double]$Bytes) {
    $units = @("B", "KB", "MB", "GB", "TB")
    $i = 0
    while ($Bytes -ge 1024 -and $i -lt 4) { $Bytes = $Bytes / 1024; $i++ }
    if ($i -eq 0) { return ('{0:N0} {1}' -f $Bytes, $units[$i]) }
    return ('{0:N1} {1}' -f $Bytes, $units[$i])
}

function Write-ProgressLine {
    param([double]$Done, [double]$Total, [double]$Speed)
    if ($Total -gt 0) {
        $pct = [int]($Done * 100 / $Total)
        if ($pct -gt 100) { $pct = 100 }
        $filled = [int]($pct * 24 / 100)
        $bar = ("#" * $filled) + ("-" * (24 - $filled))
        $line = "        [$bar] {0,3}%  {1} / {2}  {3}/s" -f $pct, (Format-Size $Done), (Format-Size $Total), (Format-Size $Speed)
    } else {
        $line = "        已下载 {0}  {1}/s" -f (Format-Size $Done), (Format-Size $Speed)
    }
    Write-Host ("`r" + $line.PadRight(96)) -NoNewline
}

function Show-Options {
    Write-Host ""
    Write-Host "安装选项" -ForegroundColor White
    Write-Host "  安装目录:     $InstallDir"
    $serverText = $Server
    if (-not $serverText) { $serverText = "（必填，未提供）" }
    Write-Host "  服务端地址:   $serverText"
    $keyText = "（必填，未提供）"
    if ($Key) { $keyText = "已提供（$($Key.Length) 位，不回显）" }
    Write-Host "  接入密码:     $keyText"
    $nodeText = $Node
    if (-not $nodeText) { $nodeText = "默认使用主机名 $env:COMPUTERNAME" }
    Write-Host "  节点名称:     $nodeText"
    Write-Host "  面板地址:     $Panel"
    $verText = $Version
    if (-not $verText) { $verText = "自动获取最新 release" }
    Write-Host "  安装版本:     $verText"
    $mirrorText = $Mirror
    if (-not $mirrorText) { $mirrorText = "自动（镜像 → 直连）" }
    Write-Host "  下载源:       $mirrorText"
    $localText = $LocalFile
    if (-not $localText) { $localText = "不使用" }
    Write-Host "  离线文件:     $localText"
    $adminText = "交互输入"
    if ($AdminPassword) { $adminText = "由 -AdminPassword 指定" }
    Write-Host "  面板密码:     $adminText"
    Write-Host "  服务名:       $service"
}

function Show-Usage {
    Write-Host ""
    Write-Host "用法" -ForegroundColor White
    Write-Host "  powershell -NoProfile -ExecutionPolicy Bypass -File .\install-client.ps1 -Server 服务端地址 -Key 接入密码 [选项]"
    Write-Host ""
    Write-Host "必填" -ForegroundColor White
    Write-Host "  -Server HOST:PORT        服务端控制面地址，如 203.0.113.10:10000"
    Write-Host "  -Key PASSWORD            接入密码（服务端管理员提供，或「加密」页为节点单独生成的密码）"
    Write-Host "                           也可用环境变量 MTUN_JOIN_TOKEN"
    Write-Host ""
    Write-Host "安装选项（其余均可省略，使用默认值）" -ForegroundColor White
    Write-Host "  -Node NAME               节点名称            默认: 主机名"
    Write-Host "  -InstallDir DIR          安装目录            默认: C:\Program Files\mtun-client（非管理员为 %USERPROFILE%\mtun-client）"
    Write-Host "  -Panel ADDR              面板监听地址        默认: 127.0.0.1:10005（远程访问用 0.0.0.0:10005）"
    Write-Host "  -AdminPassword PW        面板登录密码（至少 8 位），不交互"
    Write-Host "  -Version TAG             安装指定版本        默认: 最新 release"
    Write-Host "  -Mirror URL              GitHub 加速前缀，如 https://ghfast.top/"
    Write-Host "  -LocalFile PATH          离线安装：使用本地 exe"
    Write-Host "  -Uninstall               卸载（停服务、删自启项、删安装目录）"
    Write-Host "  -Help                    显示本帮助"
    Write-Host ""
    Write-Host "示例" -ForegroundColor White
    Write-Host "  powershell -File .\install-client.ps1 -Server 1.2.3.4:10000 -Key '接入密码' -Node office-pc"
    Write-Host "  powershell -File .\install-client.ps1 -Server 1.2.3.4:10000 -Key '接入密码' -Panel 0.0.0.0:10005"
    Write-Host "  powershell -File .\install-client.ps1 -LocalFile .\mtun-windows-amd64.exe -Server 1.2.3.4:10000 -Key '接入密码'"
    Write-Host "  powershell -File .\install-client.ps1 -Uninstall"
    Write-Host ""
    Write-Host "提示" -ForegroundColor White
    Write-Host "  安装过程中 Ctrl+C 会被忽略，脚本会继续执行，避免中断造成半安装状态。"
    Write-Host "  如确需中止，请直接关闭窗口，或在任务管理器中结束 powershell 进程。"
    Write-Host ""
}

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $pr = New-Object Security.Principal.WindowsPrincipal($id)
    return $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-Utf8NoBom {
    param([string]$Path, [string]$Value)
    [System.IO.File]::WriteAllText($Path, $Value, [System.Text.UTF8Encoding]::new($false))
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

function Get-RemoteSize {
    param([string]$Url)
    $len = 0
    try {
        $out = curl.exe -fsSIL --connect-timeout 8 --max-time 25 $Url 2>$null
        foreach ($line in $out) {
            if ($line -match '^(?i:content-length):\s*(\d+)') { $len = [int64]$Matches[1] }
        }
    } catch { $len = 0 }
    return $len
}

function Invoke-Download {
    param([string]$Url, [string]$OutFile)

    foreach ($prefix in $mirrorList) {
        $target = "$prefix$Url"
        $label = if ($prefix) { $prefix } else { "GitHub 直连" }
        Write-Info "下载源: $label"
        $total = Get-RemoteSize $target
        if ($total -gt 0) {
            Write-Detail ("文件大小: " + (Format-Size $total))
        } else {
            Write-Detail "文件大小: 未知（将按已下载量显示）"
        }

        if (Test-Path $OutFile) { Remove-Item $OutFile -Force -ErrorAction SilentlyContinue }
        $sw = [Diagnostics.Stopwatch]::StartNew()
        $argLine = "-fsSL --connect-timeout 10 --max-time 900 -o `"$OutFile`" `"$target`""
        $proc = Start-Process -FilePath "curl.exe" -ArgumentList $argLine -NoNewWindow -PassThru

        $speed = 0
        while (-not $proc.HasExited) {
            Start-Sleep -Milliseconds 500
            $size = 0
            if (Test-Path $OutFile) {
                try { $size = (Get-Item $OutFile).Length } catch { $size = 0 }
            }
            $secs = $sw.Elapsed.TotalSeconds
            if ($secs -gt 0) { $speed = $size / $secs }
            Write-ProgressLine -Done $size -Total $total -Speed $speed
        }
        $proc.WaitForExit()

        $size = 0
        if (Test-Path $OutFile) {
            try { $size = (Get-Item $OutFile).Length } catch { $size = 0 }
        }
        if ($total -le 0) { $total = $size }
        $secs = $sw.Elapsed.TotalSeconds
        if ($secs -gt 0 -and $size -gt 0) { $speed = $size / $secs }
        Write-Host ""
        if ($proc.ExitCode -eq 0 -and $size -gt 0 -and ($total -le 0 -or $size -ge $total)) {
            Write-Ok ("下载完成: {0}，平均 {1}/s" -f (Format-Size $size), (Format-Size $speed))
            return
        }
        Write-WarnLine ("该源下载失败（退出码 {0}），切换下一个源" -f $proc.ExitCode)
        Start-Sleep -Seconds 1
    }
    throw "所有下载源均失败：$Url`n可指定 -Mirror https://ghfast.top/，或使用 -LocalFile 离线安装"
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
    if ($result.message) { Write-Detail $result.message }
    Write-Ok "已启动并配置开机自启（$($result.mode)）"
}

# ---------- 参数默认值 ----------
$panelSpecified = $PSBoundParameters.ContainsKey("Panel") -or -not [string]::IsNullOrEmpty($env:MTUN_PANEL)
if (-not $Panel) {
    if ($env:MTUN_PANEL) { $Panel = $env:MTUN_PANEL } else { $Panel = "127.0.0.1:10005" }
}
if (-not $InstallDir) {
    if (Test-Admin) { $InstallDir = "C:\Program Files\mtun-client" }
    else            { $InstallDir = "$env:USERPROFILE\mtun-client" }
}
$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)

$mirrorList = if ($Mirror) { @($Mirror) } else { @("https://ghfast.top/", "https://gh-proxy.com/", "") }

$binDir = Join-Path $InstallDir "bin"
$cfgDir = Join-Path $InstallDir "configs"
$logDir = Join-Path $InstallDir "logs"
$cfg = Join-Path $cfgDir "client.yaml"
$join = "$cfg.join"
$exe = Join-Path $binDir "mtun.exe"

# ---------- 横幅 ----------
Write-Banner
if ($Help) { Show-Usage; exit 0 }
Show-Options

# ---------- Ctrl+C 防误触 ----------
$ctrlCGuarded = $false
try {
    [Console]::TreatControlCAsInput = $true
    $ctrlCGuarded = $true
    Write-Host ""
    Write-Detail "已启用 Ctrl+C 保护：安装过程中按 Ctrl+C 不会中断（如确需中止请关闭窗口）"
} catch {
    Write-WarnLine "当前会话无法启用 Ctrl+C 保护（非交互终端），请勿在安装过程中关闭窗口"
}

try {
    # ---------- 卸载 ----------
    if ($Uninstall) {
        Write-Step "卸载 $AppName 客户端"
        Write-Info "停止服务与自启项"
        Stop-InstalledInstance
        if (Test-Path $exe) {
            $removeOutput = & $exe __autostart remove -role $role -c $cfg 2>&1
            if ($LASTEXITCODE -ne 0) {
                Write-WarnLine "mtun __autostart remove 失败，将继续清理旧自启动项"
            }
        }
        Remove-AutostartArtifacts
        Write-Info "删除安装目录 $InstallDir"
        Remove-Item $InstallDir -Recurse -Force -ErrorAction SilentlyContinue
        Write-Ok "已卸载：服务、自启项、$InstallDir 均已删除"
        return
    }

    # ---------- 环境与参数 ----------
    Write-Step "检查运行环境与安装参数"
    $isAdmin = Test-Admin
    Write-Info ("管理员权限: " + $(if ($isAdmin) { "是" } else { "否（安装到用户目录，自启仅对当前用户生效）" }))
    Write-Info ("操作系统: " + (Get-CimInstance Win32_OperatingSystem).Caption)
    Write-Info ("PowerShell: " + $PSVersionTable.PSVersion)

    if (-not (Get-Command curl.exe -ErrorAction SilentlyContinue)) {
        Write-Fail "缺少 curl.exe（Windows 10/11 自带），请先安装或改用离线安装"
        throw "缺少依赖 curl.exe"
    }

    $existingServer = ""
    if (Test-Path $cfg) {
        $existingServer = Get-YamlScalar -Text ([System.IO.File]::ReadAllText($cfg)) -Name "server"
    }
    if (-not (Test-Path $cfg) -and -not $Server) {
        Write-Fail "首次安装必须提供 -Server"
        Write-Detail "例如: -Server 1.2.3.4:10000"
        throw "缺少 -Server"
    }
    if (-not (Test-Path $join) -and -not $Key) {
        Write-Fail "首次安装必须提供 -Key（或设置环境变量 MTUN_JOIN_TOKEN）"
        Write-Detail "接入密码由服务端管理员提供；多节点场景建议在服务端「加密」页为每个节点单独生成"
        throw "缺少 -Key"
    }
    if (-not $Server -and $Key -and -not $existingServer) {
        Write-Fail "已有配置中没有 server，更新接入密码时请同时提供 -Server"
        throw "缺少 -Server"
    }
    $targetServer = if ($Server) { $Server } else { $existingServer }
    Write-Ok "服务端: $targetServer"
    Write-Ok "节点名称: $(if ($Node) { $Node } else { $env:COMPUTERNAME })"

    $archText = "$env:PROCESSOR_ARCHITECTURE $env:PROCESSOR_ARCHITEW6432 $((Get-CimInstance Win32_OperatingSystem).OSArchitecture)"
    if ($archText -match "ARM64") { $tag = "mtun-windows-arm64.exe" } else { $tag = "mtun-windows-amd64.exe" }
    Write-Ok "目标资产: $tag"

    # ---------- 版本 ----------
    Write-Step "确定安装版本"
    if (-not $Version -and $LocalFile) { $Version = "offline" }
    if (-not $Version) {
        Write-Info "查询最新 release"
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
            Write-Fail "无法获取最新版本（网络不通）"
            Write-Detail "请用 -Version 指定版本，或使用 -LocalFile 离线安装"
            throw "无法获取最新版本"
        }
    }
    Write-Ok "目标版本: $Version"

    # ---------- 下载与校验 ----------
    Write-Step "下载并校验二进制"
    New-Item -ItemType Directory -Force -Path $binDir, $cfgDir, $logDir | Out-Null
    Write-Info "安装目录: $InstallDir"
    $tmp = Join-Path $env:TEMP ("mtun-client-install-" + [Guid]::NewGuid().ToString("N") + ".exe")

    if ($LocalFile) {
        Write-Info "离线安装，使用本地文件: $LocalFile"
        if (-not (Test-Path $LocalFile)) { throw "文件不存在: $LocalFile" }
        Copy-Item $LocalFile $tmp -Force
        Write-Ok ("已复制 " + (Format-Size (Get-Item $tmp).Length))
    } else {
        $url = "https://github.com/$repo/releases/download/$Version/$tag"
        Invoke-Download -Url $url -OutFile $tmp

        Write-Info "校验 SHA256"
        $sumTmp = Join-Path $env:TEMP ("mtun-client-checksums-" + [Guid]::NewGuid().ToString("N") + ".txt")
        Invoke-Download -Url "https://github.com/$repo/releases/download/$Version/checksums.txt" -OutFile $sumTmp
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
        Write-Ok ("checksum 匹配: " + $local.Substring(0, 16) + "…")
    }

    # ---------- 安装二进制 ----------
    Write-Step "安装二进制"
    Write-Info "停止可能正在运行的旧实例"
    Stop-InstalledInstance
    Write-Info "目标: $exe"
    Move-Item $tmp $exe -Force
    & $exe version
    if ($LASTEXITCODE -ne 0) { throw "mtun 二进制无法执行" }
    Write-Ok "二进制就绪"

    # ---------- 配置 ----------
    Write-Step "写入服务端地址与接入密码"
    $newConfig = -not (Test-Path $cfg)
    if ($newConfig) {
        Write-Info "生成默认配置: $cfg"
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
    } else {
        Write-Info "检测到已有配置，保留原有内容"
    }

    $yaml = [System.IO.File]::ReadAllText($cfg)
    $yaml = Set-YamlScalar -Text $yaml -Name "update_repo" -Value $repo
    if ($Node) {
        Write-Info "节点名称 → $Node"
        $yaml = Set-YamlScalar -Text $yaml -Name "node" -Value $Node
    }
    if ($newConfig -or $panelSpecified) {
        Write-Info "面板监听地址 → $Panel"
        $yaml = Set-YamlScalar -Text $yaml -Name "dashboard" -Value $Panel
    }
    Write-Utf8NoBom -Path $cfg -Value $yaml

    if ($Server -or $Key) {
        $connectArgs = @("connect", "-server", $targetServer)
        if ($Key) { $connectArgs += @("-key", $Key) }
        $connectArgs += @("-c", $cfg)
        & $exe @connectArgs
        if ($LASTEXITCODE -ne 0) { throw "写入客户端服务端地址或接入密码失败" }
    }
    Write-Ok "服务端地址与接入密码已写入（密码只保存哈希）"

    & $exe check -c $cfg -role client | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "配置文件校验失败，详情："
        & $exe check -c $cfg -role client
        throw "请修正 $cfg 后重试"
    }
    Write-Ok "配置校验通过"

    # ---------- 面板密码 ----------
    Write-Step "配置面板登录密码"
    $passFile = "$cfg.passwd"
    if (Test-Path $passFile) {
        Write-Info "面板密码已存在，跳过"
        $adminDisplay = ""
    } else {
        if ($AdminPassword) {
            if ($AdminPassword.Length -lt 8) { throw "面板密码至少 8 位" }
            Write-Info "使用 -AdminPassword 指定的密码"
            & $exe passwd -c $cfg -password $AdminPassword | Out-Null
            $adminDisplay = $AdminPassword
        } else {
            Write-Info "交互设置（至少 8 位）"
            & $exe passwd -c $cfg
            $adminDisplay = ""
        }
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path $passFile)) {
            throw "面板密码未设置，安装已终止"
        }
        Write-Ok "面板密码已保存: $passFile"
    }

    # ---------- 自启与启动 ----------
    Write-Step "配置开机自启并启动"
    Enable-Autostart

    # ---------- 状态确认 ----------
    Write-Step "确认连接状态"
    Write-Info "等待 3 秒读取状态"
    Start-Sleep -Seconds 3
    $statusOut = & $exe status -c $cfg 2>&1
    if ($LASTEXITCODE -eq 0) {
        foreach ($line in ($statusOut -split "`r?`n")) {
            if ($line.Trim()) { Write-Host "        $line" }
        }
    } else {
        Write-WarnLine "暂时无法读取状态，可稍后执行: & `"$exe`" status -c `"$cfg`""
    }

    # ---------- 汇总 ----------
    $finalYaml = [System.IO.File]::ReadAllText($cfg)
    $finalServer = Get-YamlScalar -Text $finalYaml -Name "server"
    $finalPanel = Get-YamlScalar -Text $finalYaml -Name "dashboard"
    if (-not $finalPanel) { $finalPanel = $Panel }

    Write-Host ""
    Write-Host "================================================================" -ForegroundColor Cyan
    Write-Host "  客户端安装完成" -ForegroundColor Green
    Write-Host "================================================================" -ForegroundColor Cyan
    Write-Host "  安装目录:     $InstallDir"
    Write-Host "  配置文件:     $cfg"
    Write-Host "  服务端:       $finalServer"
    Write-Host "  节点名称:     $(if ($Node) { $Node } else { $env:COMPUTERNAME })"
    Write-Host "  面板地址:     http://$finalPanel"
    Write-Host "  日志目录:     $logDir"
    if ($adminDisplay) { Write-Host "  面板密码:     $adminDisplay" }
    else { Write-Host "  面板密码:     （沿用原有密码）" }
    Write-Host "  接入密码:     已保存为哈希，无法回显"
    Write-Host "----------------------------------------------------------------" -ForegroundColor Cyan
    Write-Host "  在线更新:     & `"$exe`" update -c `"$cfg`""
    Write-Host "  自启动状态:   & `"$exe`" __autostart status -role client -c `"$cfg`""
    Write-Host "  卸载:         powershell -File install-client.ps1 -Uninstall"
    Write-Host "================================================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  注意" -ForegroundColor White
    Write-Host "  1) 转发端口必须落在服务端 publish.port_range（默认 10100-10999）内"
    Write-Host "  2) 服务端关闭「允许默认密码接入」后，本节点必须使用独立接入密码"
    if ($Panel -ne "127.0.0.1:10005") {
        Write-Host "  3) 面板已允许公网访问，请确保防火墙限制来源并使用强密码"
    }
    Write-Host ""
}
catch {
    Write-Host ""
    Write-Fail "安装失败: $($_.Exception.Message)"
    Write-Host ""
    Write-Host "  排查建议" -ForegroundColor White
    Write-Host "  1) 国内网络下载失败时加镜像: -Mirror https://ghfast.top/"
    Write-Host "  2) 或先手动下载 mtun-windows-amd64.exe，再用 -LocalFile 离线安装"
    Write-Host "  3) 需要帮助可加 -Help 查看全部选项"
    Write-Host ""
    Write-Host "  详细堆栈:" -ForegroundColor DarkGray
    Write-Host "  $($_.ScriptStackTrace)" -ForegroundColor DarkGray
    Write-Host ""
    exit 1
}
finally {
    if ($ctrlCGuarded) {
        try { [Console]::TreatControlCAsInput = $false } catch { }
    }
}
