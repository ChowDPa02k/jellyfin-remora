[CmdletBinding()]
param(
  [Parameter(Mandatory)]
  [string]$DaemonPath,
  [Parameter(Mandatory)]
  [string]$ControlPath,
  [string]$SamplePath = 'sample\config-windows.yaml',
  [string]$WorkingDirectory = $env:TEMP
)

$ErrorActionPreference = 'Stop'
$workingRoot = [IO.Path]::GetFullPath((Resolve-Path $WorkingDirectory).Path).TrimEnd('\')
$sandbox = [IO.Path]::GetFullPath((Join-Path $workingRoot ('jellyfin-remora-clean-init-' + [guid]::NewGuid())))
if (-not $sandbox.StartsWith($workingRoot + '\', [StringComparison]::OrdinalIgnoreCase)) {
  throw "unsafe clean-init test directory: $sandbox"
}

try {
  $bin = Join-Path $sandbox 'bin'
  $sample = Join-Path $sandbox 'sample'
  $dataRoot = Join-Path $sandbox 'data'
  New-Item -ItemType Directory -Force $bin, $sample | Out-Null
  $daemon = Join-Path $bin 'jellyfin-remora.exe'
  $control = Join-Path $bin 'remoractl.exe'
  Copy-Item -LiteralPath (Resolve-Path $DaemonPath).Path -Destination $daemon
  Copy-Item -LiteralPath (Resolve-Path $ControlPath).Path -Destination $control

  $preparedSample = Join-Path $sample 'config-windows.yaml'
  $content = Get-Content -LiteralPath (Resolve-Path $SamplePath).Path -Raw
  if ($daemon.Contains("'")) { throw "test daemon path cannot contain a YAML single quote: $daemon" }
  $content = $content.Replace("path: 'C:\Program Files\Jellyfin\Server'", "path: '$daemon'")
  $content = $content.Replace('REPLACE-WITH-ADMIN-PASSWORD', 'clean-init-password')
  $content = $content.Replace('REPLACE-WITH-ADMIN', 'clean-init-admin')
  [IO.File]::WriteAllText($preparedSample, $content, [Text.UTF8Encoding]::new($false))

  $volume = [IO.Path]::GetPathRoot($sandbox)
  & $control init --no-edit --sample-dir $sample --volume $volume --data-root $dataRoot
  if ($LASTEXITCODE -ne 0) { throw "remoractl init failed with exit $LASTEXITCODE" }

  $config = Join-Path $dataRoot 'config\config.yaml'
  $installer = Join-Path $dataRoot 'config\install-jellyfin-remora.ps1'
  if (-not (Test-Path -LiteralPath $config)) { throw "generated configuration is missing: $config" }
  if (-not (Test-Path -LiteralPath $installer)) { throw "generated service installer is missing: $installer" }

  $generated = Get-Content -LiteralPath $config -Raw
  $match = [regex]::Match($generated, "(?m)^\s*volume-guid:\s*'([^']+)'\s*$")
  if (-not $match.Success) { throw 'generated configuration has no single-quoted volume-guid' }
  $configuredGuid = $match.Groups[1].Value
  $mountvolGuid = (& mountvol.exe $volume /L | Out-String).Trim()
  if ($configuredGuid -ne $mountvolGuid) {
    throw "generated volume GUID $configuredGuid does not match mountvol $mountvolGuid"
  }

  $report = (& $daemon validate-config -c $config --json | Out-String).Trim() | ConvertFrom-Json
  if ($LASTEXITCODE -ne 0 -or -not $report.valid) {
    throw 'generated configuration did not pass validate-config'
  }
  if (@($report.storage | Where-Object { -not $_.healthy }).Count -ne 0) {
    throw 'generated configuration contains unhealthy storage results'
  }
  Write-Host "Clean Windows init passed on $volume with $configuredGuid"
} finally {
  if (Test-Path -LiteralPath $sandbox) {
    $resolvedSandbox = [IO.Path]::GetFullPath((Resolve-Path $sandbox).Path)
    if ($resolvedSandbox -ne $sandbox -or -not $resolvedSandbox.StartsWith($workingRoot + '\', [StringComparison]::OrdinalIgnoreCase)) {
      throw "refusing to remove unexpected clean-init directory: $resolvedSandbox"
    }
    Remove-Item -LiteralPath $resolvedSandbox -Recurse -Force
  }
}
