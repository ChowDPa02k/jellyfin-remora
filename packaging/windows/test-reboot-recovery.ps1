[CmdletBinding()]
param(
  [Parameter(Mandatory)]
  [ValidateSet('Arm', 'Verify', 'Cleanup')]
  [string]$Action,
  [string]$ControlPath = 'remoractl.exe',
  [string]$CheckpointPath = "$env:ProgramData\Jellyfin Remora\reboot-test.json",
  [int]$RecoveryTimeoutSeconds = 300
)

#Requires -RunAsAdministrator
$ErrorActionPreference = 'Stop'
$serviceName = 'JellyfinRemora'
$checkpoint = [IO.Path]::GetFullPath($CheckpointPath)

function Resolve-ControlExecutable {
  $command = Get-Command $ControlPath -ErrorAction Stop
  return $command.Source
}

function Get-BootTime {
  return (Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime()
}

function Get-RemoraStatus([string]$Control) {
  $raw = (& $Control --json status 2>$null | Out-String).Trim()
  if (-not $raw) { return $null }
  try { return $raw | ConvertFrom-Json } catch { return $null }
}

function Assert-HealthyStatus($Status) {
  if ($null -eq $Status) { throw 'Remora named-pipe status is unavailable' }
  if ($Status.state -ne 'RUNNING' -or $Status.desired_state -ne 'running' -or [int]$Status.pid -le 0) {
    throw "Remora is not fully running: state=$($Status.state) desired=$($Status.desired_state) pid=$($Status.pid)"
  }
  $storage = @($Status.storage)
  if ($storage.Count -eq 0) { throw 'Remora status contains no storage results' }
  $unhealthy = @($storage | Where-Object { -not $_.healthy })
  if ($unhealthy.Count -ne 0) {
    $targets = ($unhealthy | ForEach-Object { $_.target }) -join ', '
    throw "storage is not fully healthy: $targets"
  }
}

function Remove-Checkpoint {
  if (Test-Path -LiteralPath $checkpoint) {
    Remove-Item -LiteralPath $checkpoint -Force
  }
}

if ($Action -eq 'Cleanup') {
  Remove-Checkpoint
  Write-Host "Reboot checkpoint removed: $checkpoint"
  exit 0
}

$control = Resolve-ControlExecutable
if ($Action -eq 'Arm') {
  if (Test-Path -LiteralPath $checkpoint) {
    throw "checkpoint already exists; verify or clean it first: $checkpoint"
  }
  $service = Get-CimInstance Win32_Service -Filter "Name='$serviceName'"
  if ($null -eq $service -or $service.State -ne 'Running' -or $service.StartMode -ne 'Auto') {
    throw "$serviceName must be installed, running, and automatic before arming the reboot test"
  }
  $status = Get-RemoraStatus $control
  Assert-HealthyStatus $status
  $serviceProcess = Get-CimInstance Win32_Process -Filter "ProcessId=$($service.ProcessId)"
  if ($null -eq $serviceProcess) { throw 'cannot inspect the running Remora service process' }

  $parent = Split-Path -Parent $checkpoint
  New-Item -ItemType Directory -Force $parent | Out-Null
  $temporary = "$checkpoint.tmp"
  $checkpointJson = [ordered]@{
    machine = $env:COMPUTERNAME
    armed_at = [DateTime]::UtcNow.ToString('O')
    boot_time = (Get-BootTime).ToString('O')
    service_account = $service.StartName
    service_pid = [int]$service.ProcessId
    service_started_at = $serviceProcess.CreationDate.ToUniversalTime().ToString('O')
    daemon_username = $status.username
    jellyfin_pid = [int]$status.pid
    jellyfin_started_at = ([DateTime]$status.process_started_at).ToUniversalTime().ToString('O')
  } | ConvertTo-Json
  [IO.File]::WriteAllText($temporary, $checkpointJson, [Text.UTF8Encoding]::new($false))
  Move-Item -LiteralPath $temporary -Destination $checkpoint
  & icacls.exe $checkpoint /inheritance:r /grant:r 'SYSTEM:F' 'BUILTIN\Administrators:F' | Out-Null
  if ($LASTEXITCODE -ne 0) { throw 'failed to restrict reboot checkpoint ACL' }
  Write-Host "Reboot test armed at $checkpoint; reboot this host, then run -Action Verify."
  exit 0
}

if (-not (Test-Path -LiteralPath $checkpoint)) {
  throw "reboot checkpoint does not exist: $checkpoint"
}
$saved = Get-Content -LiteralPath $checkpoint -Raw | ConvertFrom-Json
if ($saved.machine -ne $env:COMPUTERNAME) {
  throw "checkpoint belongs to $($saved.machine), not $env:COMPUTERNAME"
}
$previousBoot = ([DateTime]$saved.boot_time).ToUniversalTime()
$currentBoot = Get-BootTime
if ($currentBoot -le $previousBoot) {
  throw "the host has not rebooted since the test was armed; boot time is still $currentBoot"
}

$deadline = (Get-Date).AddSeconds($RecoveryTimeoutSeconds)
$lastProblem = 'service has not been checked'
do {
  $service = Get-CimInstance Win32_Service -Filter "Name='$serviceName'" -ErrorAction SilentlyContinue
  if ($null -eq $service -or $service.State -ne 'Running') {
    $lastProblem = "service state is $($service.State)"
    Start-Sleep -Seconds 2
    continue
  }
  if ($service.StartName -ne $saved.service_account) {
    throw "service account changed across reboot: $($service.StartName), want $($saved.service_account)"
  }
  $status = Get-RemoraStatus $control
  try {
    Assert-HealthyStatus $status
  } catch {
    $lastProblem = $_.Exception.Message
    Start-Sleep -Seconds 2
    continue
  }
  if ($status.username -ne $saved.daemon_username) {
    throw "daemon identity changed across reboot: $($status.username), want $($saved.daemon_username)"
  }
  $serviceProcess = Get-CimInstance Win32_Process -Filter "ProcessId=$($service.ProcessId)"
  $serviceStarted = $serviceProcess.CreationDate.ToUniversalTime()
  $jellyfinStarted = ([DateTime]$status.process_started_at).ToUniversalTime()
  if ($serviceStarted -lt $currentBoot -or $jellyfinStarted -lt $currentBoot) {
    throw "service or Jellyfin process predates current boot: service=$serviceStarted Jellyfin=$jellyfinStarted boot=$currentBoot"
  }
  Write-Host "Reboot recovery passed: service PID $($service.ProcessId), Jellyfin PID $($status.pid), storage healthy."
  exit 0
} while ((Get-Date) -lt $deadline)

throw "reboot recovery did not become healthy within $RecoveryTimeoutSeconds seconds: $lastProblem"
