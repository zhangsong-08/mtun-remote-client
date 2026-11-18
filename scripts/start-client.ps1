[CmdletBinding()]
param(
    [string]$Config = "configs/client.example.yaml",
    [string]$Server = "",
    [string]$Key = ""
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

if (-not $Key) { $Key = $env:MTUN_JOIN_TOKEN }
$tokenFile = Join-Path $root "configs\join.token"
if (-not $Key -and (Test-Path $tokenFile)) {
    $Key = (Get-Content $tokenFile -Raw).Trim()
}
if ($Server -and -not $Key) {
    throw "首次配置需要客户端接入密码：请使用 -Key，或在 configs\join.token 中放置服务端生成的密码"
}

Write-Host ""
Write-Host "客户端启动中"
Write-Host "  配置文件:     $Config"
Write-Host "  控制台:       http://127.0.0.1:10005"
Write-Host "  登录密码:     首次打开控制台时由你自己设置"
if ($Server) {
    Write-Host "  自动配置服务端: $Server"
} else {
    Write-Host "  未指定 -Server，可在控制台或使用 mtun connect 添加服务端"
}
Write-Host ""

$clientArgs = @("client", "-c", $Config)
$proc = Start-Process -FilePath $exe -ArgumentList $clientArgs -PassThru -NoNewWindow

try {
    if ($Server) {
        $deadline = (Get-Date).AddSeconds(15)
        $ready = $false
        while ((Get-Date) -lt $deadline) {
            $conn = New-Object System.Net.Sockets.TcpClient
            try {
                $conn.Connect("127.0.0.1", 10005)
                $conn.Close()
                $ready = $true
                break
            } catch {
                Start-Sleep -Milliseconds 200
            } finally {
                $conn.Dispose()
            }
        }
        if (-not $ready) { throw "客户端控制台未在 10005 就绪" }

        $connectArgs = @("connect", "-server", $Server, "-c", $Config)
        if ($Key) { $connectArgs += @("-key", $Key) }
        & $exe @connectArgs
    }
    Wait-Process -Id $proc.Id
} finally {
    if (-not $proc.HasExited) {
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    }
}
