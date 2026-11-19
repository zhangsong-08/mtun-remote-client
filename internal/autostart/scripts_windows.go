// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

//go:build windows

package autostart

const statusScript = `
$ErrorActionPreference = 'Stop'
$name = $env:MTUN_AUTOSTART_NAME
$service = @(Get-CimInstance -ClassName Win32_Service | Where-Object { $_.Name -eq $name })[0]
$task = Get-ScheduledTask -TaskName $name -TaskPath '\' -ErrorAction SilentlyContinue
$startup = [Environment]::GetFolderPath('Startup')
$link = Join-Path $startup ($name + '.lnk')
$serviceExists = $null -ne $service
$startMode = ''
if ($serviceExists) { $startMode = [string]$service.StartMode }
$taskExists = $null -ne $task
$taskEnabled = $false
$taskTrigger = ''
if ($taskExists) {
    $taskEnabled = [bool]$task.Settings.Enabled
    $trigger = @($task.Triggers)[0]
    if ($null -ne $trigger) {
        $class = [string]$trigger.CimClass.CimClassName
        if ($class -match 'Boot') { $taskTrigger = 'AtStartup' }
        elseif ($class -match 'Logon') { $taskTrigger = 'AtLogOn' }
    }
}
[ordered]@{
    service_exists = $serviceExists
    service_start_mode = $startMode
    task_exists = $taskExists
    task_enabled = $taskEnabled
    task_trigger = $taskTrigger
    startup_link = Test-Path -LiteralPath $link -PathType Leaf
} | ConvertTo-Json -Compress
`

const enableServiceScript = `
$ErrorActionPreference = 'Stop'
$name = $env:MTUN_AUTOSTART_NAME
$service = @(Get-CimInstance -ClassName Win32_Service | Where-Object { $_.Name -eq $name })[0]
if ($null -eq $service) { throw 'Automatic startup service no longer exists' }
Set-Service -Name $name -StartupType Automatic
$task = Get-ScheduledTask -TaskName $name -TaskPath '\' -ErrorAction SilentlyContinue
if ($null -ne $task) { Unregister-ScheduledTask -TaskName $name -TaskPath '\' -Confirm:$false }
$link = Join-Path ([Environment]::GetFolderPath('Startup')) ($name + '.lnk')
Remove-Item -LiteralPath $link -Force -ErrorAction SilentlyContinue
@{ ok = $true } | ConvertTo-Json -Compress
`

const enableTaskScript = `
$ErrorActionPreference = 'Stop'
$name = $env:MTUN_AUTOSTART_NAME
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principalInfo = New-Object Security.Principal.WindowsPrincipal($identity)
$isAdmin = $principalInfo.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
$powershell = Join-Path $PSHOME 'powershell.exe'
$arguments = '-NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + $env:MTUN_AUTOSTART_WRAPPER + '"'
$action = New-ScheduledTaskAction -Execute $powershell -Argument $arguments
$settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
if ($isAdmin) {
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
} else {
    $user = $identity.Name
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $user
    $principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
}
Register-ScheduledTask -TaskName $name -TaskPath '\' -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
$link = Join-Path ([Environment]::GetFolderPath('Startup')) ($name + '.lnk')
Remove-Item -LiteralPath $link -Force -ErrorAction SilentlyContinue
@{ ok = $true } | ConvertTo-Json -Compress
`

const disableScript = `
$ErrorActionPreference = 'Stop'
$name = $env:MTUN_AUTOSTART_NAME
$service = @(Get-CimInstance -ClassName Win32_Service | Where-Object { $_.Name -eq $name })[0]
if ($null -ne $service) { Set-Service -Name $name -StartupType Disabled }
$task = Get-ScheduledTask -TaskName $name -TaskPath '\' -ErrorAction SilentlyContinue
if ($null -ne $task -and $task.Settings.Enabled) { Disable-ScheduledTask -TaskName $name -TaskPath '\' | Out-Null }
$link = Join-Path ([Environment]::GetFolderPath('Startup')) ($name + '.lnk')
Remove-Item -LiteralPath $link -Force -ErrorAction SilentlyContinue
@{ ok = $true } | ConvertTo-Json -Compress
`

const removeScript = `
$ErrorActionPreference = 'Stop'
$name = $env:MTUN_AUTOSTART_NAME
$service = @(Get-CimInstance -ClassName Win32_Service | Where-Object { $_.Name -eq $name })[0]
if ($null -ne $service) { Set-Service -Name $name -StartupType Disabled }
$task = Get-ScheduledTask -TaskName $name -TaskPath '\' -ErrorAction SilentlyContinue
if ($null -ne $task) { Unregister-ScheduledTask -TaskName $name -TaskPath '\' -Confirm:$false }
$link = Join-Path ([Environment]::GetFolderPath('Startup')) ($name + '.lnk')
Remove-Item -LiteralPath $link -Force -ErrorAction SilentlyContinue
@{ ok = $true } | ConvertTo-Json -Compress
`

const startServiceScript = `
$ErrorActionPreference = 'Stop'
Start-Service -Name $env:MTUN_AUTOSTART_NAME
@{ ok = $true } | ConvertTo-Json -Compress
`

const startTaskScript = `
$ErrorActionPreference = 'Stop'
Start-ScheduledTask -TaskName $env:MTUN_AUTOSTART_NAME -TaskPath '\'
@{ ok = $true } | ConvertTo-Json -Compress
`
