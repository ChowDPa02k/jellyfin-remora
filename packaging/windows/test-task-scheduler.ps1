[CmdletBinding()]
param(
  [Parameter(Mandatory)]
  [string]$InstallerPath,
  [Parameter(Mandatory)]
  [string]$ControlPath,
  [int]$TimeoutSeconds = 45
)

#Requires -RunAsAdministrator
$ErrorActionPreference = 'Stop'
$taskName = 'JellyfinRemora-User'
$serviceName = 'JellyfinRemora'
$installer = (Resolve-Path -LiteralPath $InstallerPath).Path
$control = (Resolve-Path -LiteralPath $ControlPath).Path
$daemonPath = $null
$jellyfinPID = 0
$installed = $false

if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
  throw "service already exists: $serviceName"
}
if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) {
  throw "scheduled task already exists: $taskName"
}

function Wait-Until([scriptblock]$Condition, [string]$Failure) {
  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    if (& $Condition) { return }
    Start-Sleep -Milliseconds 250
  } while ((Get-Date) -lt $deadline)
  throw $Failure
}

function Get-ControlStatus {
  $savedPreference = $ErrorActionPreference
  $ErrorActionPreference = 'SilentlyContinue'
  try {
    $raw = (& $control --json status 2>$null | Out-String).Trim()
  } finally {
    $ErrorActionPreference = $savedPreference
  }
  if (-not $raw) { return $null }
  try { return $raw | ConvertFrom-Json } catch { return $null }
}

try {
  & $installer -Action InstallTask
  if ($LASTEXITCODE -ne 0) { throw "task installer exited with $LASTEXITCODE" }
  $installed = $true
  & $installer -Action StartTask
  if ($LASTEXITCODE -ne 0) { throw "task start exited with $LASTEXITCODE" }

  Wait-Until { (Get-ControlStatus).pid -gt 0 } 'Jellyfin did not start through the scheduled task'
  $status = Get-ControlStatus
  $task = Get-ScheduledTask -TaskName $taskName
  $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
  $expectedSID = ([System.Security.Principal.NTAccount]$identity).Translate([System.Security.Principal.SecurityIdentifier]).Value
  $actualSID = ([System.Security.Principal.NTAccount]$task.Principal.UserId).Translate([System.Security.Principal.SecurityIdentifier]).Value
  if ($actualSID -ne $expectedSID) {
    throw "task principal is $($task.Principal.UserId), want $identity"
  }
  if ($task.Principal.LogonType.ToString() -ne 'Interactive') {
    throw "task logon type is $($task.Principal.LogonType), want Interactive"
  }
  if ($task.Principal.RunLevel.ToString() -ne 'Highest') {
    throw "task run level is $($task.Principal.RunLevel), want Highest"
  }
  if ($task.Actions.Count -ne 1) {
    throw "task has $($task.Actions.Count) actions, want 1"
  }

  $daemonPath = [IO.Path]::GetFullPath($task.Actions[0].Execute)
  $daemon = @(Get-CimInstance Win32_Process | Where-Object {
      $_.ExecutablePath -and [IO.Path]::GetFullPath($_.ExecutablePath) -eq $daemonPath
    })
  if ($daemon.Count -ne 1) {
    throw "task daemon process count is $($daemon.Count), want 1"
  }
  if ($status.pid) {
    $jellyfinPID = [int]$status.pid
  }

  Write-Host "Windows scheduled task passed: $identity, daemon PID $($daemon[0].ProcessId), Jellyfin PID $jellyfinPID"
} finally {
  if ($installed -or (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue)) {
    & $installer -Action UninstallTask
    if ($LASTEXITCODE -ne 0) { Write-Warning "task uninstaller exited with $LASTEXITCODE" }
  }
  Wait-Until { $null -eq (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) } 'scheduled task remained registered after uninstall'
  if ($daemonPath) {
    Wait-Until {
      -not (Get-CimInstance Win32_Process | Where-Object {
          $_.ExecutablePath -and [IO.Path]::GetFullPath($_.ExecutablePath) -eq $daemonPath
        })
    } 'scheduled task daemon remained alive after uninstall'
  }
  if ($jellyfinPID -gt 0) {
    Wait-Until { $null -eq (Get-Process -Id $jellyfinPID -ErrorAction SilentlyContinue) } 'Jellyfin remained alive after task uninstall'
  }
}
