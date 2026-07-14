//go:build windows

package main

import (
	"path/filepath"
	"strings"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func platformSampleName() (string, error) { return "config-windows.yaml", nil }
func remoraExecutableName() string        { return "jellyfin-remora.exe" }

func generatePlatformService(cfg *config.Config, executable, configPath string) (*serviceArtifact, error) {
	quote := func(value string) string { return strings.ReplaceAll(value, "'", "''") }
	writablePaths := []string{cfg.Remora.DataDir, cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir}
	quotedPaths := make([]string, 0, len(writablePaths))
	for _, path := range writablePaths {
		quotedPaths = append(quotedPaths, "'"+quote(path)+"'")
	}
	script := strings.NewReplacer(
		"{{EXE}}", quote(executable),
		"{{CONFIG}}", quote(configPath),
		"{{WRITABLE_PATHS}}", strings.Join(quotedPaths, ",\n  "),
	).Replace(windowsInstallerScript)
	path := filepath.Join(cfg.Jellyfin.ConfigDir, "install-jellyfin-remora.ps1")
	if err := atomicWriteFile(path, []byte(script), 0o600); err != nil {
		return nil, err
	}
	return &serviceArtifact{Kind: "Windows service installer", Path: path}, nil
}

const windowsInstallerScript = `#Requires -RunAsAdministrator
param(
  [ValidateSet('Install','Uninstall','InstallTask','UninstallTask')]
  [string]$Action = 'Install',
  [string]$ServiceAccount = 'NT SERVICE\JellyfinRemora',
  [System.Management.Automation.PSCredential]$ServiceCredential
)

$ErrorActionPreference = 'Stop'
$serviceName = 'JellyfinRemora'
$displayName = 'Jellyfin Remora'
$executable = '{{EXE}}'
$installDir = Split-Path -Parent $executable
$configPath = '{{CONFIG}}'
$writablePaths = @(
  {{WRITABLE_PATHS}}
)
$binaryPath = '"' + $executable + '" --service -c "' + $configPath + '"'
$taskName = 'JellyfinRemora-User'

function Grant-ServiceLogonRight([string]$Account) {
  if (-not ('Remora.LsaRights' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Security.Principal;

namespace Remora {
  public static class LsaRights {
    [StructLayout(LayoutKind.Sequential)]
    private struct LSA_OBJECT_ATTRIBUTES {
      public uint Length;
      public IntPtr RootDirectory;
      public IntPtr ObjectName;
      public uint Attributes;
      public IntPtr SecurityDescriptor;
      public IntPtr SecurityQualityOfService;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct LSA_UNICODE_STRING {
      public ushort Length;
      public ushort MaximumLength;
      public IntPtr Buffer;
    }

    [DllImport("advapi32.dll", SetLastError = true)]
    private static extern uint LsaOpenPolicy(
      IntPtr systemName,
      ref LSA_OBJECT_ATTRIBUTES objectAttributes,
      uint desiredAccess,
      out IntPtr policyHandle);

    [DllImport("advapi32.dll")]
    private static extern uint LsaAddAccountRights(
      IntPtr policyHandle,
      byte[] accountSid,
      LSA_UNICODE_STRING[] userRights,
      uint countOfRights);

    [DllImport("advapi32.dll")]
    private static extern uint LsaNtStatusToWinError(uint status);

    [DllImport("advapi32.dll")]
    private static extern uint LsaClose(IntPtr policyHandle);

    public static void AddServiceLogon(string account) {
      var sid = (SecurityIdentifier)new NTAccount(account).Translate(typeof(SecurityIdentifier));
      var sidBytes = new byte[sid.BinaryLength];
      sid.GetBinaryForm(sidBytes, 0);
      var attributes = new LSA_OBJECT_ATTRIBUTES();
      attributes.Length = (uint)Marshal.SizeOf(typeof(LSA_OBJECT_ATTRIBUTES));
      IntPtr policy;
      uint status = LsaOpenPolicy(IntPtr.Zero, ref attributes, 0x00000810, out policy);
      ThrowIfFailed(status, "LsaOpenPolicy");
      IntPtr buffer = Marshal.StringToHGlobalUni("SeServiceLogonRight");
      try {
        var right = new LSA_UNICODE_STRING[] { new LSA_UNICODE_STRING {
          Buffer = buffer,
          Length = (ushort)("SeServiceLogonRight".Length * 2),
          MaximumLength = (ushort)(("SeServiceLogonRight".Length + 1) * 2)
        }};
        ThrowIfFailed(LsaAddAccountRights(policy, sidBytes, right, 1), "LsaAddAccountRights");
      } finally {
        Marshal.FreeHGlobal(buffer);
        LsaClose(policy);
      }
    }

    private static void ThrowIfFailed(uint status, string operation) {
      if (status != 0) {
        throw new Win32Exception((int)LsaNtStatusToWinError(status), operation);
      }
    }
  }
}
'@
  }
  [Remora.LsaRights]::AddServiceLogon($Account)
}

function Set-ServiceIdentity([string]$Name, [string]$Account) {
  $service = Get-CimInstance Win32_Service -Filter "Name='$Name'"
  if ($null -eq $service) { throw "Service $Name was not created." }
  $result = Invoke-CimMethod -InputObject $service -MethodName Change -Arguments @{
    StartName = $Account
    StartPassword = $null
  }
  if ([int]$result.ReturnValue -ne 0) {
    throw "Changing service $Name to account $Account failed with Win32 error $($result.ReturnValue)."
  }
}

if ($Action -eq 'Uninstall') {
  Stop-Service -Name $serviceName -ErrorAction SilentlyContinue
  sc.exe delete $serviceName | Out-Host
  if ([System.Diagnostics.EventLog]::SourceExists($serviceName)) {
    Remove-EventLog -Source $serviceName
  }
  exit $LASTEXITCODE
}

if ($Action -eq 'UninstallTask') {
  Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
  Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
  exit 0
}

if ($Action -eq 'InstallTask') {
  if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
    throw "Service $serviceName already exists. Uninstall it before installing the scheduled task."
  }
  $taskAction = New-ScheduledTaskAction -Execute $executable -Argument ('-c "' + $configPath + '"')
  $trigger = New-ScheduledTaskTrigger -AtLogOn
  $user = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
  $principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Highest
  $settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
  Register-ScheduledTask -TaskName $taskName -Action $taskAction -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
  Start-ScheduledTask -TaskName $taskName
  Write-Host "Installed and started scheduled task $taskName for $user."
  exit 0
}

if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
  throw "Service $serviceName already exists. Run with -Action Uninstall first."
}
if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) {
  throw "Scheduled task $taskName already exists. Uninstall it before installing the service."
}

if ($null -ne $ServiceCredential) {
  if ($ServiceAccount -ne 'NT SERVICE\JellyfinRemora' -and $ServiceAccount -ne $ServiceCredential.UserName) {
    throw '-ServiceAccount must match -ServiceCredential.UserName when both are provided.'
  }
  $ServiceAccount = $ServiceCredential.UserName
  Grant-ServiceLogonRight $ServiceAccount
  New-Service -Name $serviceName -BinaryPathName $binaryPath -DisplayName $displayName -StartupType Automatic -Credential $ServiceCredential | Out-Null
} else {
  New-Service -Name $serviceName -BinaryPathName $binaryPath -DisplayName $displayName -StartupType Automatic | Out-Null
  Set-ServiceIdentity $serviceName $ServiceAccount
}
Set-ItemProperty -LiteralPath "HKLM:\SYSTEM\CurrentControlSet\Services\$serviceName" -Name Description -Value 'Supervises Jellyfin and fences it when required storage is unhealthy.'
sc.exe failure $serviceName reset= 86400 actions= restart/5000/restart/15000/restart/60000 | Out-Host
sc.exe failureflag $serviceName 1 | Out-Host
if (-not [System.Diagnostics.EventLog]::SourceExists($serviceName)) {
  New-EventLog -LogName Application -Source $serviceName
}

if (Test-Path -LiteralPath $configPath) {
  icacls.exe $configPath /grant "${ServiceAccount}:R" | Out-Host
}
if (Test-Path -LiteralPath $installDir) {
  icacls.exe $installDir /grant "${ServiceAccount}:(OI)(CI)RX" | Out-Host
}
foreach ($path in $writablePaths) {
  if (Test-Path -LiteralPath $path) {
    icacls.exe $path /grant "${ServiceAccount}:(OI)(CI)M" | Out-Host
  }
}
Start-Service -Name $serviceName
Write-Host "Installed and started $displayName."
`
