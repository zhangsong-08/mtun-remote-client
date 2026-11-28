# Copyright (c) 2026 song08
# SPDX-License-Identifier: MIT

<#
.SYNOPSIS
  MTUN 服务端安装脚本（Windows）

.DESCRIPTION
  从 GitHub Release 下载 mtun-windows-amd64.exe 或 arm64.exe，
  生成默认配置、设置管理与接入密码，并通过 mtun 统一配置开机自启。
  网络访问 GitHub 困难时自动切换国内加速镜像，也支持离线安装。

  安装过程中 Ctrl+C 会被忽略，避免把系统停在半安装状态。

  依赖：curl.exe（Win10/11 自带）。不依赖 Go。

.NOTES
  Copyright (c) 2026 song08 · SPDX-License-Identifier: MIT
  最后更新: 2026-11-28
  脚本版本: 2026.11.28

.EXAMPLE
  # 管理员 PowerShell 执行（自动选源，配置开机自启）
  powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1

.EXAMPLE
  # 国内服务器：指定镜像 + 自定义端口
  powershell -File .\install.ps1 -Mirror https://ghfast.top/ -Panel 0.0.0.0:10004 -Control :10000

.EXAMPLE
  # 离线安装（先在本机下载 exe 再传上去）
  powershell -File .\install.ps1 -LocalFile .\mtun-windows-amd64.exe

.EXAMPLE
  powershell -File .\install.ps1 -Uninstall
#>

[CmdletBinding()]
param(
    [string]$Version = "",              # 留空 = 最新 release；离线安装时为 offline
    [string]$InstallDir = "",           # 留空 = C:\Program Files\mtun 或 %USERPROFILE%\mtun
    [string]$AdminPassword = "",        # 面板管理密码；为空时交互输入
    [string]$JoinPassword = "",         # 默认接入密码；为空时保留已有或自动生成
    [string]$Panel = "",                # 面板监听地址，默认 127.0.0.1:10004
    [string]$Control = "",              # 控制面监听地址，默认 :10000
    [string]$Mirror = $env:MTUN_MIRROR, # GitHub 加速前缀，如 https://ghfast.top/
    [string]$LocalFile = "",            # 离线安装：使用本地 exe
    [switch]$UseNSSM = $true,           # 兼容旧命令行；自启方式由 mtun 统一管理
    [switch]$Uninstall = $false,
    [switch]$Yes = $false,              # 跳过交互询问，全部使用参数与默认值
    [switch]$Help = $false
)

$ErrorActionPreference = "Stop"

# ---------- 元信息 ----------
$AppName     = "MTUN"
$RoleName    = "服务端"
$Copyright   = "Copyright (c) 2026 song08 · SPDX-License-Identifier: MIT"
$LastUpdated = "2026-11-28"
$ScriptRev   = "2026.11.28"
$repo        = "zhangsong-08/mtun-remote-server"
$role        = "server"
$service     = "mtun-server"
$taskName    = "mtun-server"
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
    param([double]$Done, [double]$Total, [double]$Speed, [switch]$Finish)
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
    Write-Host "  面板地址:     $Panel"
    Write-Host "  控制面端口:   $Control"
    $verText = $Version
    if (-not $verText) { $verText = "自动获取最新 release" }
    Write-Host "  安装版本:     $verText"
    $mirrorText = $Mirror
    if (-not $mirrorText) { $mirrorText = "自动（镜像 → 直连）" }
    Write-Host "  下载源:       $mirrorText"
    $localText = $LocalFile
    if (-not $localText) { $localText = "不使用" }
    Write-Host "  离线文件:     $localText"
    $adminText = "交互询问 / 自动生成"
    if ($AdminPassword) { $adminText = "已填写" }
    Write-Host "  管理密码:     $adminText"
    $joinText = "保留已有 / 自动生成"
    if ($JoinPassword) { $joinText = "由 -JoinPassword 指定" }
    Write-Host "  接入密码:     $joinText"
    Write-Host "  服务名:       $service"
}

function Show-Usage {
    Write-Host ""
    Write-Host "用法" -ForegroundColor White
    Write-Host "  powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 [选项]"
    Write-Host ""
    Write-Host "  不加 -Yes 时进入交互式配置：逐项询问安装选项，直接回车即使用方括号内的默认值。"
    Write-Host "  非交互终端或已指定 -Yes 时，跳过询问、直接使用参数与默认值。"
    Write-Host ""
    Write-Host "安装选项（全部可省略，使用默认值）" -ForegroundColor White
    Write-Host "  -InstallDir DIR          安装目录            默认: C:\Program Files\mtun（非管理员为 %USERPROFILE%\mtun）"
    Write-Host "  -Panel ADDR              面板监听地址        默认: 127.0.0.1:10004（远程访问用 0.0.0.0:10004）"
    Write-Host "  -Control ADDR            控制面监听地址      默认: :10000"
    Write-Host "  -Version TAG             安装指定版本        默认: 最新 release"
    Write-Host "  -Mirror URL              GitHub 加速前缀，如 https://ghfast.top/"
    Write-Host "  -LocalFile PATH          离线安装：使用本地 exe"
    Write-Host "  -AdminPassword PW        面板管理密码（至少 8 位）；留空则交互询问"
    Write-Host "  -JoinPassword PW         默认接入密码（至少 8 位）；留空则保留已有或自动生成"
    Write-Host "  -Yes                     跳过交互询问，全部使用参数与默认值"
    Write-Host "  -Uninstall               卸载（停服务、删自启项、删安装目录）"
    Write-Host "  -Help                    显示本帮助"
    Write-Host ""
    Write-Host "环境变量" -ForegroundColor White
    Write-Host "  MTUN_MIRROR / MTUN_ADMIN_PASSWORD / MTUN_JOIN_PASSWORD / MTUN_PANEL / MTUN_CONTROL"
    Write-Host "  MTUN_NONINTERACTIVE=1    等同 -Yes"
    Write-Host ""
    Write-Host "示例" -ForegroundColor White
    Write-Host "  powershell -File .\install.ps1                      # 交互式填写，回车取默认"
    Write-Host "  powershell -File .\install.ps1 -Yes                  # 全默认、不询问"
    Write-Host "  powershell -File .\install.ps1 -Mirror https://ghfast.top/ -Yes"
    Write-Host "  powershell -File .\install.ps1 -Panel 0.0.0.0:10004 -Control :10000 -Yes"
    Write-Host "  powershell -File .\install.ps1 -LocalFile .\mtun-windows-amd64.exe -Yes"
    Write-Host "  powershell -File .\install.ps1 -Uninstall"
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
    throw "所有下载源均失败：$Url`n国内网络请指定 -Mirror https://ghfast.top/，或用 -LocalFile 离线安装"
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
    if ($result.message) { Write-Detail $result.message }
    Write-Ok "已启动并配置开机自启（$($result.mode)）"
}

# ---------- 交互式选项 ----------
function Read-TextOption {
    param([string]$Label, [string]$Default)
    if (-not $Interactive) { return $Default }
    $hint = if ($Default) { $Default } else { "留空=默认" }
    $value = Read-Host "  $Label [$hint]"
    if ([string]::IsNullOrWhiteSpace($value)) { return $Default }
    return $value.Trim()
}

function Read-SecretOption {
    param([string]$Label, [string]$Current, [string]$Hint)
    if (-not $Interactive) { return $Current }
    $display = $Hint
    if ($Current) { $display = "已设置，回车保持不变" }
    $sec = Read-Host "  $Label [$display]" -AsSecureString
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec)
    try {
        $plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
    if ([string]::IsNullOrWhiteSpace($plain)) { return $Current }
    return $plain.Trim()
}

function Read-InteractiveOptions {
    if (-not $Interactive) {
        Write-Detail "未检测到交互终端或已指定 -Yes，使用当前参数与默认值"
        return
    }
    Write-Host ""
    Write-Host "请填写安装选项（直接按回车使用方括号内的默认值）" -ForegroundColor White
    $script:InstallDir    = Read-TextOption "安装目录" $script:InstallDir
    $script:Panel         = Read-TextOption "面板监听地址" $script:Panel
    $script:Control       = Read-TextOption "控制面监听端口" $script:Control
    $script:JoinPassword  = Read-SecretOption "默认接入密码（留空自动生成）" $script:JoinPassword "自动生成"
    $script:AdminPassword = Read-SecretOption "面板管理密码（留空自动生成）" $script:AdminPassword "自动生成"
    $script:Version       = Read-TextOption "安装版本（如 v0.5.1）" $script:Version
    $script:Mirror        = Read-TextOption "GitHub 加速前缀（如 https://ghfast.top/）" $script:Mirror
    $script:InstallDir    = [System.IO.Path]::GetFullPath($script:InstallDir)
    Write-Host ""
}

# ---------- 参数默认值 ----------
if (-not $InstallDir) {
    if (Test-Admin) { $InstallDir = "C:\Program Files\mtun" }
    else            { $InstallDir = "$env:USERPROFILE\mtun" }
}
$InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
if (-not $Panel)   { $Panel = "127.0.0.1:10004" }
if (-not $Control) { $Control = ":10000" }

$Interactive = $true
if ($Yes -or $env:MTUN_NONINTERACTIVE -or -not [Environment]::UserInteractive) { $Interactive = $false }
try { if ([Console]::IsInputRedirected) { $Interactive = $false } } catch { }

# ---------- 横幅与选项 ----------
Write-Banner
if ($Help) { Show-Usage; exit 0 }

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

# ---------- 交互填写与生效配置 ----------
if (-not $Uninstall) { Read-InteractiveOptions }
Show-Options

$mirrorList = if ($Mirror) { @($Mirror) } else { @("https://ghfast.top/", "https://gh-proxy.com/", "") }
$binDir = Join-Path $InstallDir "bin"
$cfgDir = Join-Path $InstallDir "configs"
$logDir = Join-Path $InstallDir "logs"
$cfg = Join-Path $cfgDir "server.yaml"
$exe = Join-Path $binDir "mtun.exe"

try {
    # ---------- 卸载 ----------
    if ($Uninstall) {
        Write-Step "卸载 $AppName 服务端"
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

    # ---------- 环境 ----------
    Write-Step "检查运行环境"
    $isAdmin = Test-Admin
    Write-Info ("管理员权限: " + $(if ($isAdmin) { "是" } else { "否（安装到用户目录，自启仅对当前用户生效）" }))
    Write-Info ("操作系统: " + (Get-CimInstance Win32_OperatingSystem).Caption)
    Write-Info ("PowerShell: " + $PSVersionTable.PSVersion)

    if (-not (Get-Command curl.exe -ErrorAction SilentlyContinue)) {
        Write-Fail "缺少 curl.exe（Windows 10/11 自带），请先安装或改用离线安装"
        throw "缺少依赖 curl.exe"
    }
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
    $tmp = Join-Path $env:TEMP ("mtun-install-" + [Guid]::NewGuid().ToString("N") + ".exe")

    if ($LocalFile) {
        Write-Info "离线安装，使用本地文件: $LocalFile"
        if (-not (Test-Path $LocalFile)) { throw "文件不存在: $LocalFile" }
        Copy-Item $LocalFile $tmp -Force
        Write-Ok ("已复制 " + (Format-Size (Get-Item $tmp).Length))
    } else {
        $url = "https://github.com/$repo/releases/download/$Version/$tag"
        Invoke-Download -Url $url -OutFile $tmp

        Write-Info "校验 SHA256"
        $sumTmp = Join-Path $env:TEMP ("mtun-checksums-" + [Guid]::NewGuid().ToString("N") + ".txt")
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
    Write-Step "生成并校验配置"
    if (-not (Test-Path $cfg)) {
        Write-Info "生成默认配置: $cfg"
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
    } else {
        Write-Info "检测到已有配置，保留原有内容"
    }

    $yaml = [System.IO.File]::ReadAllText($cfg)
    $yaml = Set-YamlScalar -Text $yaml -Name "update_repo" -Value $repo
    if ($Panel -ne "127.0.0.1:10004") {
        Write-Info "面板监听地址 → $Panel"
        $yaml = Set-YamlScalar -Text $yaml -Name "dashboard" -Value $Panel
    }
    if ($Control -ne ":10000") {
        Write-Info "控制面端口 → $Control"
        $yaml = Set-YamlScalar -Text $yaml -Name "control" -Value $Control
    }
    Write-Utf8NoBom -Path $cfg -Value $yaml

    & $exe check -c $cfg -role server | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "配置文件校验失败，详情："
        & $exe check -c $cfg -role server
        throw "请修正 $cfg 后重试"
    }
    Write-Ok "配置校验通过: $cfg"

    # ---------- 接入密码 ----------
    Write-Step "配置客户端接入密码"
    $join = "$cfg.join"
    if (-not $JoinPassword -and (Test-Path $join)) {
        Write-Info "检测到已有接入密码，保持不变"
        $joinDisplay = ""
    } elseif ($JoinPassword) {
        if ($JoinPassword.Length -lt 8) { throw "接入密码至少 8 位" }
        & $exe access -c $cfg -password $JoinPassword | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "写入客户端接入密码失败" }
        Write-Ok "已设置为 -JoinPassword 指定的密码"
        $joinDisplay = $JoinPassword
    } else {
        $bytes = New-Object byte[] 18
        [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes) | Out-Null
        $JoinPassword = -join ($bytes | ForEach-Object { $_.ToString("x2") })
        & $exe access -c $cfg -password $JoinPassword | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "写入客户端接入密码失败" }
        Write-Ok "已随机生成 36 位接入密码"
        $joinDisplay = $JoinPassword
    }
    Write-Detail "密码文件: $join（只保存哈希，明文不落盘）"

    # ---------- 管理密码 ----------
    Write-Step "配置面板管理密码"
    $passFile = "$cfg.passwd"
    if (Test-Path $passFile) {
        Write-Info "管理密码已存在，跳过"
        $adminDisplay = ""
    } else {
        if ($AdminPassword) {
            if ($AdminPassword.Length -lt 8) { throw "管理密码至少 8 位" }
            Write-Info "使用交互输入的密码"
            & $exe passwd -c $cfg -password $AdminPassword | Out-Null
            $adminDisplay = $AdminPassword
        } else {
            $bytes = New-Object byte[] 16
            [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes) | Out-Null
            $AdminPassword = -join ($bytes | ForEach-Object { $_.ToString("x2") })
            & $exe passwd -c $cfg -password $AdminPassword | Out-Null
            Write-Info "未填写，已自动生成管理密码"
            $adminDisplay = $AdminPassword
        }
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path $passFile)) {
            throw "管理密码未设置，安装已终止"
        }
        Write-Ok "管理密码已保存: $passFile"
    }

    # ---------- 自启与启动 ----------
    Write-Step "配置开机自启并启动"
    Enable-Autostart

    # ---------- 汇总 ----------
    Write-Host ""
    Write-Host "================================================================" -ForegroundColor Cyan
    Write-Host "  安装完成" -ForegroundColor Green
    Write-Host "================================================================" -ForegroundColor Cyan
    Write-Host "  安装目录:       $InstallDir"
    Write-Host "  配置文件:       $cfg"
    Write-Host "  面板地址:       http://$Panel"
    Write-Host "  控制面端口:     $Control（TLS 1.3）"
    Write-Host "  转发端口范围:   10100-10999"
    Write-Host "  日志目录:       $logDir"
    if ($adminDisplay) { Write-Host "  管理密码:       $adminDisplay" }
    else { Write-Host "  管理密码:       （沿用原有密码）" }
    if ($joinDisplay) { Write-Host "  接入密码:       $joinDisplay" }
    else {
        Write-Host "  接入密码:       （沿用原有密码，已保存哈希）"
        Write-Host "                  需重置请执行: & `"$exe`" access -c `"$cfg`""
    }
    Write-Host "----------------------------------------------------------------" -ForegroundColor Cyan
    Write-Host "  在线更新:       & `"$exe`" update -c `"$cfg`""
    Write-Host "  自启动状态:     & `"$exe`" __autostart status -role server -c `"$cfg`""
    Write-Host "  卸载:           powershell -File install.ps1 -Uninstall"
    Write-Host "================================================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  注意" -ForegroundColor White
    Write-Host "  1) 公网服务器请放行防火墙/安全组：10000/TCP、10100-10999/TCP"
    Write-Host "  2) 面板默认只监听本机；远程访问请用 SSH 隧道或 -Panel 0.0.0.0:10004"
    Write-Host "  3) 新增设备时把接入密码填给客户端安装脚本 -Key 参数"
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
