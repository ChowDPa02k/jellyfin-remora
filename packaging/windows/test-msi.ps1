[CmdletBinding()]
param(
  [Parameter(Mandatory)]
  [string]$OldMsi,
  [Parameter(Mandatory)]
  [string]$NewMsi,
  [string]$OldVersion = '0.6.0-alpha',
  [string]$NewVersion = '0.6.1-alpha',
  [string]$InstallDirectory = "$env:ProgramFiles\Jellyfin Remora"
)

#Requires -RunAsAdministrator
$ErrorActionPreference = 'Stop'
$old = (Resolve-Path $OldMsi).Path
$new = (Resolve-Path $NewMsi).Path
$logs = Join-Path $env:TEMP ("jellyfin-remora-msi-test-" + [guid]::NewGuid())
New-Item -ItemType Directory $logs | Out-Null

function Invoke-Msi([string[]]$Arguments, [string]$Name, [int[]]$AllowedExitCodes = @(0)) {
  $process = Start-Process msiexec.exe -ArgumentList $Arguments -Wait -PassThru -WindowStyle Hidden
  if ($process.ExitCode -notin $AllowedExitCodes) {
    throw "$Name failed with exit code $($process.ExitCode); logs: $logs"
  }
  Write-Host "$Name exit=$($process.ExitCode)"
  return $process.ExitCode
}

function Assert-DaemonVersion([string]$Daemon, [string]$ExpectedVersion) {
  $reported = (& $Daemon --version | Out-String).Trim()
  if ($LASTEXITCODE -ne 0 -or $reported -notmatch [regex]::Escape(" $ExpectedVersion ")) {
    throw "expected daemon version $ExpectedVersion, got: $reported"
  }
}

if (Test-Path -LiteralPath $InstallDirectory) {
  throw "Refusing to overwrite existing installation directory: $InstallDirectory"
}

try {
  $null = Invoke-Msi @('/i', "`"$old`"", '/qn', '/norestart', '/l*v', "`"$(Join-Path $logs 'install.log')`"") 'install-old'
  $daemon = Join-Path $InstallDirectory 'jellyfin-remora.exe'
  if (-not (Test-Path -LiteralPath $daemon)) { throw 'daemon missing after install' }
  Assert-DaemonVersion $daemon $OldVersion

  $null = Invoke-Msi @('/fa', "`"$old`"", '/qn', '/norestart', '/l*v', "`"$(Join-Path $logs 'repair.log')`"") 'repair-old'
  $null = Invoke-Msi @('/i', "`"$new`"", 'WIXFAILWHENDEFERRED=1', '/qn', '/norestart', '/l*v', "`"$(Join-Path $logs 'rollback-upgrade.log')`"") 'rollback-upgrade' @(1603)
  Assert-DaemonVersion $daemon $OldVersion
  $null = Invoke-Msi @('/i', "`"$new`"", '/qn', '/norestart', '/l*v', "`"$(Join-Path $logs 'upgrade.log')`"") 'upgrade-new'
  Assert-DaemonVersion $daemon $NewVersion
  $null = Invoke-Msi @('/i', "`"$old`"", '/qn', '/norestart', '/l*v', "`"$(Join-Path $logs 'downgrade.log')`"") 'block-downgrade' @(1603, 1638)
} finally {
  $null = Invoke-Msi @('/x', "`"$new`"", '/qn', '/norestart', '/l*v', "`"$(Join-Path $logs 'uninstall-new.log')`"") 'uninstall-new' @(0, 1605)
  $null = Invoke-Msi @('/x', "`"$old`"", '/qn', '/norestart', '/l*v', "`"$(Join-Path $logs 'uninstall-old.log')`"") 'uninstall-old' @(0, 1605)
}

if (Test-Path -LiteralPath $InstallDirectory) {
  throw "installation directory remains after uninstall: $InstallDirectory"
}
Write-Host "MSI lifecycle passed; logs: $logs"
