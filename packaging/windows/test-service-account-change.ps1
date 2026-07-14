[CmdletBinding()]
param(
  [Parameter(Mandatory)]
  [string]$DaemonPath,
  [Parameter(Mandatory)]
  [string]$ControlPath,
  [Parameter(Mandatory)]
  [string]$ConfigPath,
  [string[]]$WritablePaths = @()
)

#Requires -RunAsAdministrator
$ErrorActionPreference = 'Stop'
$serviceName = 'JellyfinRemora'
$firstAccount = 'NT SERVICE\JellyfinRemora'
$secondAccount = 'NT AUTHORITY\LocalService'
$programData = [IO.Path]::GetFullPath($env:ProgramData).TrimEnd('\')
$sandbox = [IO.Path]::GetFullPath((Join-Path $programData 'Jellyfin Remora Phase4 Service Test'))
if (-not $sandbox.StartsWith($programData + '\', [StringComparison]::OrdinalIgnoreCase)) {
  throw "unsafe service test directory: $sandbox"
}
if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
  throw "service already exists: $serviceName"
}

function Invoke-Sc([string[]]$Arguments, [int[]]$AllowedExitCodes = @(0)) {
  $output = (& sc.exe @Arguments 2>&1 | Out-String).Trim()
  if ($LASTEXITCODE -notin $AllowedExitCodes) {
    throw "sc.exe $($Arguments -join ' ') failed with exit $LASTEXITCODE`: $output"
  }
  return $output
}

function Wait-ServiceState([string]$Expected, [int]$TimeoutSeconds = 30) {
  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    $service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
    if ($null -ne $service -and $service.Status.ToString() -eq $Expected) {
      return
    }
    Start-Sleep -Milliseconds 200
  } while ((Get-Date) -lt $deadline)
  throw "service did not reach $Expected within $TimeoutSeconds seconds"
}

function Wait-ControlStatus([string]$Control, [int]$TimeoutSeconds = 30) {
  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    $raw = (& $Control --json status 2>$null | Out-String).Trim()
    if ($raw) {
      try {
        return $raw | ConvertFrom-Json
      } catch {
      }
    }
    Start-Sleep -Milliseconds 200
  } while ((Get-Date) -lt $deadline)
  throw "named-pipe status was unavailable within $TimeoutSeconds seconds"
}

function Grant-ServiceAccess([string]$Account, [string]$Config, [string]$InstallDirectory) {
  & icacls.exe $Config /grant "${Account}:R" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "failed to grant config access to $Account" }
  & icacls.exe $InstallDirectory /grant "${Account}:(OI)(CI)RX" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "failed to grant executable access to $Account" }
  foreach ($path in $WritablePaths) {
    $resolved = [IO.Path]::GetFullPath($path)
    & icacls.exe $resolved /grant "${Account}:(OI)(CI)M" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "failed to grant writable access to $Account on $resolved" }
  }
}

$created = $false
try {
  New-Item -ItemType Directory -Force $sandbox | Out-Null
  $daemon = Join-Path $sandbox 'jellyfin-remora.exe'
  $control = Join-Path $sandbox 'remoractl.exe'
  $config = Join-Path $sandbox 'config.yaml'
  Copy-Item -LiteralPath (Resolve-Path $DaemonPath).Path -Destination $daemon
  Copy-Item -LiteralPath (Resolve-Path $ControlPath).Path -Destination $control
  Copy-Item -LiteralPath (Resolve-Path $ConfigPath).Path -Destination $config
  & icacls.exe $config /inheritance:r /grant:r 'SYSTEM:F' 'BUILTIN\Administrators:F' | Out-Null
  if ($LASTEXITCODE -ne 0) { throw 'failed to restrict copied configuration ACL' }

  $binaryPath = "`"$daemon`" --service -c `"$config`""
  $null = Invoke-Sc @('create', $serviceName, 'binPath=', $binaryPath, 'start=', 'demand', 'obj=', $firstAccount, 'DisplayName=', 'Jellyfin Remora Phase 4 Test')
  $created = $true
  Grant-ServiceAccess $firstAccount $config $sandbox
  $null = Invoke-Sc @('start', $serviceName)
  Wait-ServiceState 'Running'
  $firstStatus = Wait-ControlStatus $control
  $firstService = Get-CimInstance Win32_Service -Filter "Name='$serviceName'"
  if ($firstService.StartName -ne $firstAccount) {
    throw "first service identity is $($firstService.StartName), want $firstAccount"
  }

  $null = Invoke-Sc @('stop', $serviceName)
  Wait-ServiceState 'Stopped'
  $null = Invoke-Sc @('config', $serviceName, 'obj=', $secondAccount, 'password=', '')
  Grant-ServiceAccess $secondAccount $config $sandbox
  $null = Invoke-Sc @('start', $serviceName)
  Wait-ServiceState 'Running'
  $secondStatus = Wait-ControlStatus $control
  $secondService = Get-CimInstance Win32_Service -Filter "Name='$serviceName'"
  if ($secondService.StartName -ne $secondAccount) {
    throw "second service identity is $($secondService.StartName), want $secondAccount"
  }
  if ($firstStatus.username -eq $secondStatus.username) {
    throw "daemon username did not change: $($firstStatus.username)"
  }

  Write-Host "Windows service account change passed: $($firstStatus.username) -> $($secondStatus.username)"
} finally {
  if ($created) {
    $service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
    if ($null -ne $service -and $service.Status -ne 'Stopped') {
      $null = Invoke-Sc @('stop', $serviceName) @(0, 1062)
      try { Wait-ServiceState 'Stopped' 30 } catch { Write-Warning $_ }
    }
    $null = Invoke-Sc @('delete', $serviceName) @(0, 1060)
    $deadline = (Get-Date).AddSeconds(15)
    while ((Get-Service -Name $serviceName -ErrorAction SilentlyContinue) -and (Get-Date) -lt $deadline) {
      Start-Sleep -Milliseconds 200
    }
  }
  if (Test-Path -LiteralPath $sandbox) {
    $resolvedSandbox = [IO.Path]::GetFullPath((Resolve-Path $sandbox).Path)
    if ($resolvedSandbox -ne $sandbox -or -not $resolvedSandbox.StartsWith($programData + '\', [StringComparison]::OrdinalIgnoreCase)) {
      throw "refusing to remove unexpected service test directory: $resolvedSandbox"
    }
    Remove-Item -LiteralPath $resolvedSandbox -Recurse -Force
  }
}
