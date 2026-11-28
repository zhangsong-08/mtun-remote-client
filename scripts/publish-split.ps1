# Copyright (c) 2026 song08
# SPDX-License-Identifier: MIT
#
# 按角色把当前 main 拆分成「服务端仓」与「客户端仓」两份快照并推送。
#
# 用法:
#   powershell -File .\scripts\publish-split.ps1            # 只生成，不推送
#   powershell -File .\scripts\publish-split.ps1 -Push      # 生成并推送到两个远端 main
#
# 说明:
#   - 两个仓库共享同一个 Go 模块，可执行文件同时包含服务端与客户端命令，
#     因此 Go 源码需完整保留；这里裁剪的是各端专属的文档、安装脚本与配置示例。
#   - 每次运行都会基于 main 重建 split/<role> 分支，提交后以快进方式推送到对应仓库。

param(
    [string]$Root = (Resolve-Path "$PSScriptRoot\..").Path,
    [switch]$Push
)

$ErrorActionPreference = "Stop"

$roles = @(
    @{
        Name    = "server"
        Repo    = "zhangsong-08/mtun-remote-server"
        Readme  = "README.server.md"
        Exclude = @(
            "README.client.md",
            "scripts/install-client.sh",
            "scripts/install-client.ps1",
            "scripts/start-client.ps1",
            "configs/client.example.yaml"
        )
    },
    @{
        Name    = "client"
        Repo    = "zhangsong-08/mtun-remote-client"
        Readme  = "README.client.md"
        Exclude = @(
            "README.server.md",
            "scripts/install.sh",
            "scripts/install.ps1",
            "scripts/start-server.ps1",
            "configs/server.example.yaml"
        )
    }
)

function Invoke-Git {
    param([string]$WorkDir, [string[]]$GitArgs, [switch]$AllowFail)
    $output = & git -C $WorkDir @GitArgs 2>&1
    $code = $LASTEXITCODE
    if ($code -ne 0 -and -not $AllowFail) {
        throw ("git {0} 失败（{1}）：{2}" -f ($GitArgs -join " "), $code, ($output -join "`n"))
    }
    return $output
}

$gitDir = Join-Path $Root ".git"
if (-not (Test-Path $gitDir)) { throw "当前目录不是 git 仓库: $Root" }

$baseBranch = (& git -C $Root rev-parse --abbrev-ref HEAD).Trim()
if ($baseBranch -ne "main") { throw "请先切到 main 再执行（当前: $baseBranch）" }

$status = (& git -C $Root status --porcelain)
if ($status) { throw "工作区有未提交改动，请先提交后再拆分" }

$baseSha = (& git -C $Root rev-parse --short HEAD).Trim()
$stamp = (Get-Date -Format "yyyy-MM-dd HH:mm")
Write-Host "基准提交: $baseSha    时间: $stamp"

foreach ($role in $roles) {
    $name = $role.Name
    $branch = "split/$name"
    $tree = Join-Path $Root ".split/$name"
    Write-Host ""
    Write-Host "==> 生成 $name 仓库快照"

    & git -C $Root worktree remove --force $tree 2>$null | Out-Null
    & git -C $Root branch -D $branch 2>$null | Out-Null
    Invoke-Git $Root @("worktree", "add", "-B", $branch, $tree, "main") | Out-Null

    foreach ($rel in $role.Exclude) {
        $path = Join-Path $tree $rel
        if (Test-Path $path) {
            Remove-Item $path -Recurse -Force
            Write-Host ("    移除 {0}" -f $rel)
        }
    }

    Copy-Item (Join-Path $tree $role.Readme) (Join-Path $tree "README.md") -Force
    Write-Host ("    README.md ← {0}" -f $role.Readme)
    Remove-Item (Join-Path $tree $role.Readme) -Force

    Invoke-Git $tree @("add", "-A") | Out-Null
    $msg = "chore(split): 生成 $name 仓库快照（基于 $baseSha）"
    Invoke-Git $tree @("commit", "-m", $msg, "--allow-empty") | Out-Null

    $files = (& git -C $tree ls-files | Measure-Object).Count
    Write-Host ("    文件数: {0}" -f $files)

    if ($Push) {
        Write-Host ("    推送到 {0} (main)" -f $role.Repo)
        Invoke-Git $tree @("push", "https://github.com/$($role.Repo).git", "$($branch):main")
        Write-Host "    推送完成"
    } else {
        Write-Host "    未推送（加 -Push 生效）"
    }

    & git -C $Root worktree remove --force $tree | Out-Null
}

Write-Host ""
Write-Host "完成。两个仓库只保留各自角色的 README、安装脚本与配置示例；Go 源码因共用同一模块而完整保留。"
