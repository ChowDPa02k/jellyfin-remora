# Windows deployment

`jellyfin-remora.exe` supports the Windows Service Control Manager directly.
`remoractl init` writes `install-jellyfin-remora.ps1` beside the resulting
configuration. Review the script, then run it from an elevated PowerShell.

## Native service

```powershell
Set-ExecutionPolicy -Scope Process Bypass
& 'D:\jellyfin\config\install-jellyfin-remora.ps1' -Action Install
Get-Service JellyfinRemora
```

The default identity is the restricted virtual account
`NT SERVICE\JellyfinRemora`. The installer grants that identity read access to
the configuration and modify access to Remora's state directory. It registers
the `JellyfinRemora` source in the Windows Application event log, configures
automatic startup and bounded service recovery, then starts the service.

Use a deliberate service account when SMB storage needs account credentials.
Pass a `PSCredential` so PowerShell sends the password to the Service Control
Manager without storing it in the generated script:

```powershell
$credential = Get-Credential 'DOMAIN\jellyfin-remora'
& '.\install-jellyfin-remora.ps1' -Action Install `
    -ServiceCredential $credential
```

The installer grants the account the logon-as-a-service right through the LSA
API; domain policy may still override that local assignment. The account must
have the required filesystem ACLs and share permissions. Explorer mappings and
Credential Manager entries
from the interactive operator are not inherited. Validate storage while running
as the intended identity before relying on the service. Windows SMB YAML must
not contain `user` or `password`; use the account's network identity or create a
Generic Credential Manager entry while running `cmdkey /generic` under that exact account, as
described in `windows-storage.md`.

After startup, inspect the daemon identity and storage result from an elevated
terminal:

```powershell
remoractl --json status
Get-WinEvent -FilterHashtable @{LogName='Application'; ProviderName='JellyfinRemora'} -MaxEvents 20
```

The status `username` must be the configured service account, and every required
SMB entry must be mounted, reachable, and healthy before Jellyfin starts.

To remove the service and its Event Log source:

```powershell
& '.\install-jellyfin-remora.ps1' -Action Uninstall
```

An elevated development host can verify an SCM account change, ACL update,
service restart, and named-pipe recovery without retaining the test service:

```powershell
.\packaging\windows\test-service-account-change.ps1 `
    -DaemonPath .\jellyfin-remora.exe `
    -ControlPath .\remoractl.exe `
    -ConfigPath .\config.yaml `
    -WritablePaths 'D:\jellyfin-test'
```

The script refuses to run when a `JellyfinRemora` service already exists. It
uses a guarded temporary directory beneath `ProgramData` and removes the service
and copied configuration in `finally` cleanup.

## Reboot recovery gate

On a disposable host where the installed service and every configured storage
entry are already healthy, arm the reboot test from an elevated terminal:

```powershell
.\packaging\windows\test-reboot-recovery.ps1 -Action Arm `
    -ControlPath .\remoractl.exe
Restart-Computer
```

After signing in again, verify recovery and then remove the owner-only checkpoint:

```powershell
.\packaging\windows\test-reboot-recovery.ps1 -Action Verify `
    -ControlPath .\remoractl.exe
.\packaging\windows\test-reboot-recovery.ps1 -Action Cleanup
```

Verification requires a later OS boot time, automatic/running SCM state, the
same service and daemon identities, service and Jellyfin processes created after
the new boot, named-pipe control, and every storage result healthy. `Arm` never
reboots the machine itself.

## Task Scheduler compatibility

For a workstation deployment that intentionally runs only while the current
user is logged in:

```powershell
& '.\install-jellyfin-remora.ps1' -Action InstallTask
Get-ScheduledTask -TaskName JellyfinRemora-User
```

The task runs at logon, at highest privilege, under the current interactive
identity. This can see that identity's credentials, but mapped drive visibility
can still differ across elevated and non-elevated tokens. Remora continues to
verify and reconnect the configured UNC through MPR rather than trusting the
presence of a drive letter.

Remove it with:

```powershell
& '.\install-jellyfin-remora.ps1' -Action UninstallTask
```

The installer stops a running task before unregistering it and rejects attempts
to install the service and task together. A disposable elevated workstation can
exercise the complete task path with:

```powershell
.\packaging\windows\test-task-scheduler.ps1 `
    -InstallerPath D:\jellyfin\config\install-jellyfin-remora.ps1 `
    -ControlPath .\remoractl.exe
```

The harness requires an interactive login for the task principal. It verifies
the principal SID, interactive/highest execution settings, one daemon process,
named-pipe status with a nonzero Jellyfin PID, and removal of both processes
after uninstall. Do not install both deployment modes manually; the
duplicate-instance lock remains a final defense, not the deployment policy.

## Release artifacts

The repository builds Windows artifacts with:

```powershell
dotnet tool restore
.\packaging\windows\package.ps1 -Version 0.6.0-alpha -BuildMsi
```

Without `-CertificateThumbprint`, ZIP/MSI names contain `-unsigned`, and the JSON
manifest records `signed: false`. Such files are development artifacts, not a
release. A release build supplies a code-signing certificate thumbprint; the
script signs and verifies both executables before archiving and signs the MSI.
The certificate must exist in exactly one CurrentUser or LocalMachine personal
store, include a private key and the Code Signing EKU. The script discovers the
x64 Windows SDK `signtool.exe` when it is not on `PATH`; use `-SignToolPath` to
select another trusted SDK installation explicitly.

A transient self-signed certificate can test the mechanics on a disposable
development host, including RFC3161 timestamps and ZIP/MSI byte verification,
but it never satisfies the release-certificate gate.

```powershell
.\packaging\windows\test-authenticode.ps1
```

The harness creates a unique four-hour CurrentUser certificate, trusts it only
for the duration of the test, and removes it by exact thumbprint in `finally`.

The MSI installs binaries, the Windows sample, documentation, and license under
Program Files. Run `remoractl init` after installation, then install the service
using its generated script. This keeps storage selection and credentials out of
the MSI transaction.

An elevated disposable test machine can exercise install, repair, an injected
upgrade failure with transactional restoration of the old binary, major upgrade,
downgrade blocking, and uninstall with:

```powershell
.\packaging\windows\test-msi.ps1 -OldMsi .\old.msi -NewMsi .\new.msi
```
