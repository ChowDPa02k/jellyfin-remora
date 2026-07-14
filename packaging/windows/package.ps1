[CmdletBinding()]
param(
  [Parameter(Mandatory)]
  [ValidatePattern('^\d+\.\d+\.\d+([-.+][0-9A-Za-z.-]+)?$')]
  [string]$Version,
  [string]$OutputDirectory = 'build\windows',
  [switch]$BuildMsi,
  [string]$CertificateThumbprint,
  [string]$SignToolPath,
  [string]$TimestampUrl = 'http://timestamp.digicert.com'
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$output = [IO.Path]::GetFullPath((Join-Path $repo $OutputDirectory))
$stage = Join-Path $output 'stage'
Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $stage, (Join-Path $stage 'sample'), (Join-Path $stage 'docs') | Out-Null

$commit = (& git -C $repo rev-parse --short=12 HEAD 2>$null)
if (-not $commit) { $commit = 'unknown' }
$epoch = if ($env:SOURCE_DATE_EPOCH) { [long]$env:SOURCE_DATE_EPOCH } else { [DateTimeOffset]::UtcNow.ToUnixTimeSeconds() }
$buildDate = [DateTimeOffset]::FromUnixTimeSeconds($epoch).UtcDateTime.ToString('yyyy-MM-ddTHH:mm:ssZ')
$buildInfo = 'github.com/ChowDPa02K/jellyfin-remora/internal/buildinfo'
$ldflags = "-s -w -X $buildInfo.Version=$Version -X $buildInfo.Commit=$commit -X $buildInfo.Date=$buildDate"

$oldGoos, $oldGoarch, $oldCgo = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
try {
  $env:GOOS = 'windows'
  $env:GOARCH = 'amd64'
  $env:CGO_ENABLED = '0'
  & go build -trimpath -ldflags $ldflags -o (Join-Path $stage 'jellyfin-remora.exe') (Join-Path $repo 'cmd\jellyfin-remora')
  if ($LASTEXITCODE -ne 0) { throw 'jellyfin-remora build failed' }
  & go build -trimpath -ldflags $ldflags -o (Join-Path $stage 'remoractl.exe') (Join-Path $repo 'cmd\remoractl')
  if ($LASTEXITCODE -ne 0) { throw 'remoractl build failed' }
} finally {
  $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $oldGoos, $oldGoarch, $oldCgo
}

Copy-Item (Join-Path $repo 'sample\config-windows.yaml') (Join-Path $stage 'sample')
Copy-Item (Join-Path $repo 'docs\windows-storage.md'), (Join-Path $repo 'docs\windows-service.md') (Join-Path $stage 'docs')
Copy-Item (Join-Path $repo 'LICENSE') $stage

function Resolve-SignTool {
  if ($SignToolPath) {
    return (Resolve-Path -LiteralPath $SignToolPath).Path
  }
  $command = Get-Command signtool.exe -ErrorAction SilentlyContinue
  if ($command) { return $command.Source }
  $kitsRoot = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
  $candidates = @(Get-ChildItem -LiteralPath $kitsRoot -Directory -ErrorAction SilentlyContinue |
      Sort-Object { try { [version]$_.Name } catch { [version]'0.0' } } -Descending |
      ForEach-Object { Join-Path $_.FullName 'x64\signtool.exe' } |
      Where-Object { Test-Path -LiteralPath $_ })
  if ($candidates.Count -eq 0) {
    throw 'signtool.exe was not found on PATH or in the Windows 10 SDK; install the Windows SDK or pass -SignToolPath'
  }
  return $candidates[0]
}

$signtool = $null
$certificateStoreArgument = @()
if ($CertificateThumbprint) {
  $thumbprint = $CertificateThumbprint.Replace(' ', '').ToUpperInvariant()
  $userCertificates = @(Get-ChildItem Cert:\CurrentUser\My | Where-Object Thumbprint -eq $thumbprint)
  $machineCertificates = @(Get-ChildItem Cert:\LocalMachine\My | Where-Object Thumbprint -eq $thumbprint)
  if ($userCertificates.Count + $machineCertificates.Count -ne 1) {
    throw "code-signing certificate $thumbprint must exist in exactly one CurrentUser or LocalMachine personal store"
  }
  $certificate = @($userCertificates + $machineCertificates)[0]
  if (-not $certificate.HasPrivateKey) {
    throw "code-signing certificate $thumbprint has no private key"
  }
  if (-not ($certificate.EnhancedKeyUsageList | Where-Object ObjectId -eq '1.3.6.1.5.5.7.3.3')) {
    throw "certificate $thumbprint is not valid for code signing"
  }
  if ($machineCertificates.Count -eq 1) { $certificateStoreArgument = @('/sm') }
  $signtool = Resolve-SignTool
}

function Invoke-AuthenticodeSign([string]$Path) {
  if (-not $CertificateThumbprint) { return }
  & $signtool sign @certificateStoreArgument /sha1 $thumbprint /fd SHA256 /td SHA256 /tr $TimestampUrl $Path
  if ($LASTEXITCODE -ne 0) { throw "Authenticode signing failed: $Path" }
  & $signtool verify /pa /all $Path
  if ($LASTEXITCODE -ne 0) { throw "Authenticode verification failed: $Path" }
}

Invoke-AuthenticodeSign (Join-Path $stage 'jellyfin-remora.exe')
Invoke-AuthenticodeSign (Join-Path $stage 'remoractl.exe')

$fixedTime = [DateTimeOffset]::FromUnixTimeSeconds($epoch).UtcDateTime
Get-ChildItem -LiteralPath $stage -Recurse -File | ForEach-Object { $_.LastWriteTimeUtc = $fixedTime }
$suffix = if ($CertificateThumbprint) { '' } else { '-unsigned' }
$baseName = "jellyfin-remora-$Version-windows-amd64$suffix"
$zip = Join-Path $output "$baseName.zip"
Remove-Item -LiteralPath $zip -Force -ErrorAction SilentlyContinue
Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -CompressionLevel Optimal

$artifacts = @($zip)
if ($BuildMsi) {
  $numericVersion = ($Version -split '[-+]')[0]
  $msi = Join-Path $output "$baseName.msi"
  & dotnet tool run wix extension add --global WixToolset.Util.wixext/6.0.2
  if ($LASTEXITCODE -ne 0) { throw 'WiX Util extension restore failed' }
  & dotnet tool run wix build (Join-Path $repo 'packaging\windows\Product.wxs') `
      -arch x64 -ext WixToolset.Util.wixext/6.0.2 `
      -d "StageDir=$stage" -d "ProductVersion=$numericVersion" -o $msi
  if ($LASTEXITCODE -ne 0) { throw 'WiX MSI build failed' }
  Invoke-AuthenticodeSign $msi
  $artifacts += $msi
}

$checksums = Join-Path $output "$baseName.sha256"
$artifacts | ForEach-Object {
  $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $_).Hash.ToLowerInvariant()
  "$hash  $([IO.Path]::GetFileName($_))"
} | Set-Content -LiteralPath $checksums -Encoding ascii

$manifest = [ordered]@{
  version = $Version
  commit = $commit
  build_date = $buildDate
  target = 'windows/amd64'
  signed = [bool]$CertificateThumbprint
  artifacts = @($artifacts | ForEach-Object { [IO.Path]::GetFileName($_) })
} | ConvertTo-Json
[IO.File]::WriteAllText(
  (Join-Path $output "$baseName.manifest.json"),
  $manifest,
  [Text.UTF8Encoding]::new($false)
)

Write-Host "Windows artifacts written to $output"
