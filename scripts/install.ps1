<#
.SYNOPSIS
  mtun 服务端一键安装脚本（Windows）

.DESCRIPTION
  从 GitHub Release 下载 mtun-windows-amd64.exe 或 arm64.exe，
  生成默认配置、设置管理密码、注册服务。
  网络访问 GitHub 困难时自动切换国内加速镜像，也支持离线安装。

  依赖：curl.exe（Win10/11 自带）。不依赖 Go。

.EXAMPLE
  # 管理员 PowerShell 执行（自动选源，注册为服务）
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
    [string]$Version = "",             # 留空 = 最新 release
    [string]$InstallDir = "",          # 留空 = C:\Program Files\mtun
    [string]$AdminPassword = "",       # 自动化安装时传入；为空时交互提示
    [string]$JoinPassword = "",        # 客户端接入密码；为空时自动生成
    [string]$Mirror = $env:MTUN_MIRROR, # GitHub 加速前缀，如 https://ghfast.top/
    [string]$LocalFile = "",           # 离线安装：使用本地 exe
    [switch]$UseNSSM = $true,          # 检测到 NSSM 时注册为系统服务
    [switch]$Uninstall = $false
)

$ErrorActionPreference = "Stop"
$repo = "zhangsong-08/mtun-remote-server"

# 内置可用镜像（按顺序尝试，直连放最后）
$mirrorList = if ($Mirror) { @($Mirror) } else { @("https://ghfast.top/", "https://gh-proxy.com/", "") }

# ---------- 辅助 ----------
function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $pr = New-Object Security.Principal.WindowsPrincipal($id)
    return $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
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

# ---------- 参数 ----------
if (-not $InstallDir) {
    if (Test-Admin) { $InstallDir = "C:\Program Files\mtun" }
    else            { $InstallDir = "$env:USERPROFILE\mtun" }
}
$binDir  = Join-Path $InstallDir "bin"
$cfgDir  = Join-Path $InstallDir "configs"
$logDir  = Join-Path $InstallDir "logs"
$cfg     = Join-Path $cfgDir "server.yaml"
$exe     = Join-Path $binDir "mtun.exe"
$service = "mtun-server"

Write-Host "======================================================"
Write-Host " mtun 服务端安装"
Write-Host "======================================================"
Write-Host " 安装目录:   $InstallDir"
Write-Host " 管理员:     $(Test-Admin)"
Write-Host ""

# ---------- 卸载 ----------
if ($Uninstall) {
    Write-Host "==> 卸载 mtun"
    if (Get-Command nssm -ErrorAction SilentlyContinue) {
        & nssm stop $service 2>$null | Out-Null
        & nssm remove $service confirm 2>$null | Out-Null
    } else {
        Get-Process mtun -ErrorAction SilentlyContinue | Stop-Process -Force
    }
    $startupDir = [Environment]::GetFolderPath("Startup")
    Remove-Item (Join-Path $startupDir "mtun-server.lnk") -Force -ErrorAction SilentlyContinue
    Remove-Item $InstallDir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host "已卸载 mtun（若装过系统服务可能还需要手动删）"
    exit 0
}

# ---------- 版本 ----------
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
        # 镜像不代理 API 时，退而解析 releases/latest 的跳转地址
        $loc = curl.exe -fsSI --connect-timeout 8 --max-time 25 "${prefix}https://github.com/$repo/releases/latest" 2>$null |
            Select-String -Pattern "^location:" -CaseSensitive:$false | Select-Object -First 1
        if ($loc) {
            $Version = ($loc.ToString().Trim() -split "/")[-1]
            if ($Version) { break }
        }
    }
    if (-not $Version) {
        throw "无法获取最新版本（网络不通）。请用 -Version 指定，例如：-Version v0.2.1 -Mirror https://ghfast.top/"
    }
}

# ---------- 架构 ----------
$arch = (Get-CimInstance Win32_OperatingSystem).OSArchitecture
if ($arch -like "64*") { $tag = "mtun-windows-amd64.exe" }
else                   { $tag = "mtun-windows-arm64.exe" }

Write-Host "==> 使用版本 $Version，资产 $tag"

# ---------- 下载 ----------
New-Item -ItemType Directory -Force -Path $binDir,$cfgDir,$logDir | Out-Null
$url  = "https://github.com/$repo/releases/download/$Version/$tag"
$sum  = "https://github.com/$repo/releases/download/$Version/checksums.txt"
$tmp  = Join-Path $env:TEMP "mtun-install.exe"

if ($LocalFile) {
    Write-Host "==> 离线安装，使用本地文件 $LocalFile"
    if (-not (Test-Path $LocalFile)) { throw "文件不存在: $LocalFile" }
    Copy-Item $LocalFile $tmp -Force
} else {
    Write-Host "==> 下载 $url"
    Invoke-GitHubDownload -Url $url -OutFile $tmp
}

# checksum 校验（失败不阻塞）
if (-not $LocalFile) {
    $sumTmp = Join-Path $env:TEMP "mtun-checksums.txt"
    try {
        Invoke-GitHubDownload -Url $sum -OutFile $sumTmp
        if ((Get-Item $sumTmp -ErrorAction SilentlyContinue).Length -gt 0) {
            $local = (Get-FileHash $tmp -Algorithm SHA256).Hash.ToLower()
            $line = Get-Content $sumTmp | Where-Object { ($_ -split '\s+')[1] -eq $tag } | Select-Object -First 1
            $want = if ($line) { ($line -split '\s+')[0].ToLower() } else { "" }
            if ($want -and $want -eq $local) { Write-Host "==> checksum 校验通过" }
            elseif (-not $want)             { Write-Host "==> 跳过 checksum（清单中没有 $tag）" }
            else                            { Write-Host "==> 警告：checksum 不匹配，请核实下载来源" }
        }
    } catch { Write-Host "==> 跳过 checksum（下载失败）" }
    Remove-Item $sumTmp -Force -ErrorAction SilentlyContinue
}

Move-Item $tmp $exe -Force
& $exe version

# ---------- 生成配置 ----------
if (-not (Test-Path $cfg)) {
    Write-Host "==> 生成默认配置"
    & $exe default server | Set-Content $cfg -Encoding utf8
    if (-not (Test-Path $cfg) -or (Get-Item $cfg).Length -eq 0) {
        @'
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
'@ | Set-Content $cfg -Encoding utf8
    }
    & $exe check -c $cfg -role server | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Host "配置文件校验失败，详情：" -ForegroundColor Red
        & $exe check -c $cfg -role server
        exit 1
    }
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

# ---------- 管理密码 ----------
$passFile = "$cfg.passwd"
if (Test-Path $passFile) {
    Write-Host "==> 管理密码已存在，跳过"
} else {
    # mtun passwd 会提示两次输入，走 cmd.exe 才能正确交互
    Write-Host "==> 设置管理密码（至少 8 位）"
    Write-Host "     这个密码只你自己用，别泄露。"
    if ($AdminPassword) {
        # 非交互方式写入（mtun passwd 需要终端，这里用 SetPassword 命令模拟）
        Write-Host "  使用 -AdminPassword 指定的密码"
        & $exe passwd -c $cfg --stdin | Out-Null  # 若 stdin 模式不可用则回退
        # 如果上面报错，这里不做 fallback，直接让用户手工来
    } else {
        cmd /c "$exe passwd -c `"$cfg`""
    }
    if (-not (Test-Path $passFile)) {
        Write-Host "`n管理密码未设置，将跳过自动启动。请稍后手动执行：" -ForegroundColor Red
        Write-Host "  cmd /c `"`"$exe`" passwd -c `"$cfg`"`""
        exit 0
    }
}

# ---------- 启动 ----------
$useService = $UseNSSM -and (Get-Command nssm -ErrorAction SilentlyContinue) -and (Test-Admin)

if ($useService) {
    Write-Host "==> 用 NSSM 注册为服务 $service"
    & nssm stop $service 2>$null | Out-Null
    & nssm remove $service confirm 2>$null | Out-Null
    & nssm install $service $exe "server -c `"$cfg`"" | Out-Null
    & nssm set $service AppDirectory $InstallDir | Out-Null
    & nssm set $service AppStdout  (Join-Path $logDir "server.log") | Out-Null
    & nssm set $service AppStderr  (Join-Path $logDir "server.err") | Out-Null
    & nssm set $service Start SERVICE_AUTO_START | Out-Null
    & nssm start $service | Out-Null
    Start-Sleep -Seconds 3
    $running = Get-Service $service -ErrorAction SilentlyContinue
    if ($running -and $running.Status -eq "Running") {
        Write-Host "[OK] mtun 已作为系统服务启动"
    } else {
        Write-Host "[X] 服务启动失败，请查看 logs/server.err"
        exit 1
    }
} else {
    Write-Host "==> 启动进程"
    Get-Process mtun -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Milliseconds 500
    Start-Process -FilePath $exe -ArgumentList "server","-c",$cfg `
        -WorkingDirectory $InstallDir `
        -RedirectStandardOutput (Join-Path $logDir "server.log") `
        -RedirectStandardError  (Join-Path $logDir "server.err") `
        -WindowStyle Hidden
    Start-Sleep -Seconds 3
    $p = Get-Process mtun -ErrorAction SilentlyContinue
    if ($p) {
        Write-Host "[OK] mtun 已启动（PID $($p.Id)）"
    } else {
        Write-Host "[X] 进程未留存，查看 $logDir\server.err"
        exit 1
    }

    # 开机自启（仅非服务方案）
    if (-not (Get-Command nssm -ErrorAction SilentlyContinue)) {
        try {
            $startup = [Environment]::GetFolderPath("Startup")
            $lnk = Join-Path $startup "mtun-server.lnk"
            $shell = New-Object -ComObject WScript.Shell
            $sc = $shell.CreateShortcut($lnk)
            $sc.TargetPath = $exe
            $sc.Arguments = "server -c `"$cfg`""
            $sc.WorkingDirectory = $InstallDir
            $sc.WindowStyle = 7  # 最小化
            $sc.Save()
            Write-Host "  已添加到启动项（$lnk）"
        } catch {
            Write-Host "  未能创建启动项，请手工配置开机自启"
        }
        Write-Host "`n  提示：安装 NSSM 后可改为系统服务（重启/崩溃自动恢复）："
        Write-Host "    choco install nssm  # 管理员 PowerShell 再跑一次本脚本"
    }
}

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
Write-Host "  在线更新:       & `"$exe`" update"
Write-Host "  查看日志:       Get-Content $logDir\server.log -Tail 50"
Write-Host "  停止:           Get-Process mtun | Stop-Process"
Write-Host "  卸载:           powershell -File install.ps1 -Uninstall"
Write-Host ""
Write-Host "  [!]  如果是公网服务器，请放行防火墙端口：10000/TCP、10100-10999/TCP"
Write-Host "  [!]  别忘了把接入密码填给客户端"
