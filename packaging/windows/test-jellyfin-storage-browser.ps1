[CmdletBinding(DefaultParameterSetName = 'Credential')]
param(
  [string]$BaseUrl = 'http://127.0.0.1:8096',
  [Parameter(Mandatory, ParameterSetName = 'Credential')]
  [System.Management.Automation.PSCredential]$Credential,
  [Parameter(Mandatory, ParameterSetName = 'Token')]
  [string]$ApiToken,
  [Parameter(Mandatory)]
  [string[]]$Path
)

$ErrorActionPreference = 'Stop'
$base = $BaseUrl.TrimEnd('/')

if ($PSCmdlet.ParameterSetName -eq 'Credential') {
  $authHeader = 'MediaBrowser Client="Jellyfin%20Remora%20Test", Device="Windows", DeviceId="remora-storage-browser", Version="1.0"'
  $networkCredential = $Credential.GetNetworkCredential()
  $body = @{
    Username = $networkCredential.UserName
    Pw = $networkCredential.Password
  } | ConvertTo-Json
  try {
    $authentication = Invoke-RestMethod `
        -Method Post `
        -Uri "$base/Users/AuthenticateByName" `
        -Headers @{ Authorization = $authHeader } `
        -ContentType 'application/json' `
        -Body $body `
        -TimeoutSec 15
  } finally {
    $body = $null
    $networkCredential = $null
  }
  $ApiToken = $authentication.AccessToken
  if (-not $ApiToken) { throw 'Jellyfin authentication returned no access token' }
}

$headers = @{ 'X-Emby-Token' = $ApiToken }
$drives = Invoke-RestMethod -Method Get -Uri "$base/Environment/Drives" -Headers $headers -TimeoutSec 15
$drivePaths = @($drives.Path | Where-Object { $_ })
$results = @()
foreach ($configuredPath in $Path) {
  $normalized = [IO.Path]::GetFullPath($configuredPath)
  if (-not ($drivePaths | Where-Object { [IO.Path]::GetFullPath($_) -eq $normalized })) {
    throw "Jellyfin drive browser does not include $normalized; reported drives: $($drivePaths -join ', ')"
  }
  $encoded = [uri]::EscapeDataString($normalized)
  $contents = Invoke-RestMethod `
      -Method Get `
      -Uri "$base/Environment/DirectoryContents?path=$encoded&includeFiles=false&includeDirectories=true" `
      -Headers $headers `
      -TimeoutSec 15
  $entryPaths = @($contents.Path | Where-Object { $_ })
  $results += [pscustomobject]@{
    Path = $normalized
    BrowseSucceeded = $true
    DirectoryCount = $entryPaths.Count
  }
}

[pscustomobject]@{
  Drives = $drivePaths
  Paths = $results
} | ConvertTo-Json -Depth 4
