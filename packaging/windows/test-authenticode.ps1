[CmdletBinding()]
param(
  [string]$Version = '0.6.2-alpha',
  [string]$OutputDirectory = 'build\windows-signing-test',
  [string]$TimestampUrl = 'http://timestamp.digicert.com'
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$subject = 'CN=Jellyfin Remora Temporary Signing Test ' + [guid]::NewGuid()
$certificate = $null
$certificateFile = Join-Path $env:TEMP ('jellyfin-remora-signing-' + [guid]::NewGuid() + '.cer')
$extract = Join-Path $env:TEMP ('jellyfin-remora-signing-' + [guid]::NewGuid())

try {
  $certificate = New-SelfSignedCertificate `
      -Type CodeSigningCert `
      -Subject $subject `
      -CertStoreLocation Cert:\CurrentUser\My `
      -KeyAlgorithm RSA `
      -KeyLength 2048 `
      -HashAlgorithm SHA256 `
      -NotAfter (Get-Date).AddHours(4)
  Export-Certificate -Cert $certificate -FilePath $certificateFile | Out-Null
  Import-Certificate -FilePath $certificateFile -CertStoreLocation Cert:\CurrentUser\Root | Out-Null

  & (Join-Path $PSScriptRoot 'package.ps1') `
      -Version $Version `
      -OutputDirectory $OutputDirectory `
      -BuildMsi `
      -CertificateThumbprint $certificate.Thumbprint `
      -TimestampUrl $TimestampUrl
  if ($LASTEXITCODE -ne 0) { throw "signed package build exited with $LASTEXITCODE" }

  $output = [IO.Path]::GetFullPath((Join-Path $repo $OutputDirectory))
  $manifestPath = @(Get-ChildItem -LiteralPath $output -Filter '*.manifest.json')
  $zip = @(Get-ChildItem -LiteralPath $output -Filter '*.zip')
  $msi = @(Get-ChildItem -LiteralPath $output -Filter '*.msi')
  if ($manifestPath.Count -ne 1 -or $zip.Count -ne 1 -or $msi.Count -ne 1) {
    throw 'signed package build did not produce exactly one manifest, ZIP, and MSI'
  }
  $manifest = Get-Content -LiteralPath $manifestPath[0].FullName -Raw | ConvertFrom-Json
  if (-not $manifest.signed -or $manifest.artifacts.Count -ne 2) {
    throw 'signed package manifest is not marked signed with two artifacts'
  }

  $stageExecutables = @(Get-ChildItem -LiteralPath (Join-Path $output 'stage') -Filter '*.exe')
  foreach ($artifact in @($stageExecutables + $msi)) {
    $signature = Get-AuthenticodeSignature -LiteralPath $artifact.FullName
    if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $certificate.Thumbprint) {
      throw "invalid Authenticode signature on $($artifact.FullName): $($signature.StatusMessage)"
    }
    if (-not $signature.TimeStamperCertificate) {
      throw "Authenticode signature has no RFC3161 timestamp: $($artifact.FullName)"
    }
  }

  Expand-Archive -LiteralPath $zip[0].FullName -DestinationPath $extract
  foreach ($artifact in $stageExecutables) {
    $archived = Join-Path $extract $artifact.Name
    if (-not (Test-Path -LiteralPath $archived)) {
      throw "ZIP is missing signed executable $($artifact.Name)"
    }
    $stageHash = (Get-FileHash -LiteralPath $artifact.FullName -Algorithm SHA256).Hash
    $archiveHash = (Get-FileHash -LiteralPath $archived -Algorithm SHA256).Hash
    if ($stageHash -ne $archiveHash) {
      throw "ZIP executable differs from signed stage bytes: $($artifact.Name)"
    }
  }

  Write-Host "Temporary Authenticode path passed for $Version. This is not a release certificate test."
} finally {
  if ($certificate) {
    foreach ($store in 'Root', 'TrustedPublisher', 'My') {
      & certutil.exe -user -delstore $store $certificate.Thumbprint 2>$null | Out-Null
    }
  }
  Remove-Item -LiteralPath $certificateFile -Force -ErrorAction SilentlyContinue
  Remove-Item -LiteralPath $extract -Recurse -Force -ErrorAction SilentlyContinue
}
