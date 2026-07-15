# Windows physical-volume identification

> Status: `volume-guid`, `filesystem`, `volume-label`, `probe-path`, and native
> volume discovery in `remoractl init` are implemented. The `mountvol` and
> PowerShell procedures remain the independent verification and troubleshooting
> paths.

Remora must identify a physical volume independently of its drive letter. Windows
can reassign a drive letter after a disk is removed, another disk is attached, or
mount configuration changes. A drive letter is therefore an access path; the
volume GUID path is the configured identity.

The expected identity has this form:

```text
\\?\Volume{8d63858a-6ae4-44bf-b0ed-c3a00f467af4}\
```

Microsoft documents volume GUID paths and drive-letter mount points in
[Naming a Volume](https://learn.microsoft.com/windows/win32/fileio/naming-a-volume).

## Recommended workflow: Remora discovery

Run `remoractl init` in a terminal. It enumerates local fixed and removable
volumes and displays:

- drive letter and mounted-folder paths;
- volume label and filesystem;
- total and free capacity;
- volume GUID path.

Select the volume by number using its familiar drive letter or mounted folder,
label, filesystem, and capacity. Remora writes the GUID and correctly escaped
target into the temporary YAML template, updates the Jellyfin data directories,
then opens the configured editor for review. The validated result is written as
`remora-config.yaml` in the invocation directory, beside the generated service
installer.

```powershell
remoractl init
```

For unattended preparation, specify the familiar mount point instead of copying
a GUID:

```powershell
remoractl init --volume 'D:\' --editor notepad.exe
```

Automated image preparation can supply a fully reviewed sample and skip the
editor. `--data-root` must be an absolute directory beneath the selected volume;
the volume root itself and another volume are rejected:

```powershell
remoractl init --sample-dir .\prepared-sample `
    --volume 'D:\' --data-root 'D:\jellyfin' --no-edit
```

`--no-edit` refuses samples containing any `REPLACE-WITH` placeholder. It is
intended for CI or configuration-management input that has already supplied all
operator values, not as a way to accept the repository sample unchanged.

The selected volume must currently have a drive letter or mounted-folder path.
Before creating missing Jellyfin directories, Remora resolves that target back
to the discovered volume GUID and verifies every configured storage entry. It
refuses to create directories outside configured storage.

## Manual workflow for a drive letter

The examples below use drive `D:` with label `STORAGE`. Replace `D` with the
actual drive letter.

### 1. Confirm that the drive is the intended volume

In File Explorer, open **This PC** and confirm the drive letter, label, and
approximate capacity. Do not select a volume from the drive letter alone.

Then open PowerShell and run:

```powershell
Get-Volume -DriveLetter D |
    Format-List DriveLetter, FileSystemLabel, FileSystem, HealthStatus, Size, SizeRemaining
```

For the example disk, the important results are label `STORAGE`, filesystem
`NTFS`, and a total size of approximately 1 TB.

### 2. Obtain the volume GUID path

Use the Windows built-in `mountvol` command:

```powershell
mountvol D:\ /L
```

Expected output shape:

```text
\\?\Volume{8d63858a-6ae4-44bf-b0ed-c3a00f467af4}\
```

Use `D:\`, including the trailing backslash. `D:` alone is a drive-relative path
and is not the volume root. The `/L` behavior is documented by Microsoft in
[`mountvol`](https://learn.microsoft.com/windows-server/administration/windows-commands/mountvol).

Do not remove the output's final backslash when placing it in configuration.

### 3. PowerShell/CIM alternative

The same mapping can be queried through CIM:

```powershell
$volume = Get-CimInstance Win32_Volume -Filter "DriveLetter = 'D:'"
$volume | Format-List DeviceID, DriveLetter, Label, FileSystem, Capacity, FreeSpace
```

`DeviceID` should contain the same `\\?\Volume{GUID}\` value returned by
`mountvol D:\ /L`. Stop if the command returns no volume or more than one result;
recheck the drive letter instead of guessing.

To inspect all local fixed volumes when the drive letter is unknown:

```powershell
Get-CimInstance Win32_Volume |
    Where-Object DriveType -eq 3 |
    Sort-Object DriveLetter, Label |
    Format-Table DeviceID, DriveLetter, Label, FileSystem, Capacity, FreeSpace -AutoSize
```

### 4. Put the values in YAML

Use YAML single-quoted strings for Windows paths so backslashes remain literal:

```yaml
disk:
  - type: physical
    volume-guid: '\\?\Volume{8d63858a-6ae4-44bf-b0ed-c3a00f467af4}\'
    target: 'D:\'
    filesystem: NTFS
    volume-label: STORAGE
    probe-path: 'D:\jellyfin-test'
    permission: rw
    heartbeat: 1
    failure-threshold: 1
```

Do not write `target: 'D:'`; it is not an absolute root path. `probe-path`
must be an existing directory beneath the verified target where the Windows
service identity can create, flush, and delete a temporary file. A volume root
often denies this to non-administrative users, so use the Jellyfin data parent
rather than weakening the root ACL. Double-quoted YAML also works only if every
backslash is escaped, so single quotes are preferred.

### 5. Verify the mapping

Before starting Jellyfin, run `mountvol` again and compare its entire output with
`volume-guid`:

```powershell
$expected = '\\?\Volume{8d63858a-6ae4-44bf-b0ed-c3a00f467af4}\'
$actual = (mountvol D:\ /L).Trim()
if ($actual -ne $expected) {
    throw "D:\ is not the configured physical volume. Actual identity: $actual"
}
```

Then validate the configuration with the daemon binary before starting it:

```powershell
jellyfin-remora validate-config -c 'C:\ProgramData\Jellyfin Remora\config.yaml' --json
remoractl diagnose
```

Validation must fail closed when the configured target is missing or resolves to
a different volume GUID.

## Volumes mounted in a folder

A physical volume can be mounted at an NTFS folder instead of a drive letter. For
example, if Explorer or Disk Management shows the mount point
`C:\Mount\Storage\`, query it with:

```powershell
mountvol C:\Mount\Storage\ /L
```

Use the mounted folder as the target:

```yaml
target: 'C:\Mount\Storage\'
```

The configured GUID remains the identity. Microsoft documents that drive
letters, volume GUID paths, and mounted folders are all volume mount-point forms,
and that `GetVolumeNameForVolumeMountPoint` resolves a mount point to a volume
GUID path in
[Mounted Folder Functions](https://learn.microsoft.com/windows/win32/fileio/volume-mount-point-functions).

A volume with no drive letter or mounted folder has no ordinary filesystem path
for Jellyfin. Assign a drive letter or mounted folder in Disk Management before
using it as a Remora `target`; `remoractl init` displays it as `(not mounted)`
but refuses to select it. Do not invent a target path.

## If the drive letter changes

Suppose the configured volume moves from `D:\` to `E:\`:

1. Stop and leave Jellyfin fenced.
2. Run `mountvol E:\ /L` and confirm that it returns the configured volume GUID.
3. Confirm the label, filesystem, and capacity with `Get-Volume -DriveLetter E`.
4. Change only `target` from `'D:\'` to `'E:\'`; do not replace the GUID.
5. Validate the configuration and let the normal recovery-success threshold pass.

Remora must never treat a different disk that later receives `D:` as healthy.
Automatic configuration rewriting is unsafe and is not part of recovery.

## Identifiers that must not be substituted

- **Volume GUID path**: `\\?\Volume{GUID}\`; this is the Remora physical-volume identity.
- **Partition GUID**: identifies a GPT partition. It can look identical in some tools but is a different concept and field.
- **Volume serial number**: filesystem metadata such as an NTFS serial; useful diagnostically but not the configured primary identity.
- **Device path**: `\Device\HarddiskVolume6`; the numeric suffix is assigned by Windows and is not a persistent configuration identifier.
- **Drive letter**: `D:\`; an access path that can change or be reused by another disk.

If DiskGenius is used, copy the value labeled **GUID path**, not **partition GUID**,
**volume GUID**, **volume serial number**, or **device path**. Confirm it with
`mountvol <drive>:\ /L` before using it.

## SMB and NFS are different

The procedure above applies only to `type: physical`. SMB uses a UNC source such
as `\\server\share` and Windows service credentials. A drive mapped in an
interactive login session may not exist for the Remora service account. NFS also
depends on the optional Windows NFS client. Neither network-storage type should
be assigned a physical `volume-guid`.

### Why Explorer and the service can show different drives

Windows drive mappings belong to a logon token, not to the machine as a whole.
Explorer, an elevated terminal, Task Scheduler, and a Windows service can each
see a different set of drive letters even when they run under accounts with the
same displayed name. Therefore, an Explorer mapping is useful evidence but is
not sufficient for a service deployment.

The reverse is also expected: a drive that Remora connects in service Session 0
normally does not appear in the desktop Explorer session. Refreshing Explorer or
waiting longer does not merge those sessions. Jellyfin launched by Remora runs
in the same service session and can use that mapping even though Explorer cannot
display it.

Inspect the mapping in the same non-elevated terminal that starts Remora:

```powershell
Get-CimInstance Win32_LogicalDisk -Filter "DeviceID = 'F:'" |
    Format-List DeviceID, ProviderName, DriveType
cmd.exe /c net use F:
```

The provider must match the configured source after normalizing slash direction:

```yaml
- type: smb
  device: //192.168.1.20/nas_STORAGE_公共空间
  target: 'F:\'
  credential: windows-credential-manager
  permission: rw
  heartbeat: 3
  failure-threshold: 3
```

Remora first queries `F:` through the Windows Multiple Provider Router. If the
mapping is absent in its token, it reads the server entry from the exact service
account's Windows Credential Manager and passes it directly to
`WNetAddConnection2` for the configured UNC and drive letter. No password is read
from YAML or written to Remora logs. Windows configurations containing SMB
`user` or `password` are rejected rather than silently exposing or ignoring
credentials.

For an interactive-account test, create and verify a credential as that account:

```powershell
cmdkey.exe /generic:192.168.1.20 /user:nas /pass:REPLACE_WITH_PASSWORD
cmdkey.exe /list:192.168.1.20
```

Do not put the password on a shared command line or in shell history on a
multi-user machine. Prefer the Windows Credential Manager UI for manual entry.
The generated Windows service uses the virtual account
`NT SERVICE\JellyfinRemora`; an interactive user's Credential Manager entries
and mapped drives are not inherited by that account. Before deploying SMB under
the service, choose and test a deliberate service identity that can authenticate
to the share. If that account does not authenticate to the share directly, open
a credential command under that exact account and let `cmdkey` prompt for the
share password:

```powershell
runas.exe /profile /user:DOMAIN\jellyfin-remora `
    'cmd.exe /k cmdkey.exe /generic:192.168.1.20 /user:nas'
```

Use `/generic`, not `/add`. The latter creates a Domain Password entry whose
password blob is not available to Remora through `CredReadW`; a Generic entry is
encrypted by Windows for the service account and can be supplied to MPR without
placing it in YAML or logs.

Then run the generated installer with a matching `-ServiceCredential`. The
Service Control Manager loads the configured service account's profile; it does
not use the interactive administrator's Credential Manager vault.

To prove which identity and token are being tested, run:

```powershell
whoami.exe
jellyfin-remora validate-config -c .\config.yaml --json
```

The SMB result must report the configured `device`, `target`, `mounted: true`,
and `reachable: true`. A different UNC already occupying `F:` is a fatal
configuration error; Remora will not disconnect or replace it automatically.

For media-library selection, verify the view from Jellyfin itself. The web
folder picker uses these server-side APIs; supply a temporary Jellyfin API token
without writing it to the configuration or command history:

```powershell
$headers = @{ 'X-Emby-Token' = $env:JELLYFIN_API_TOKEN }
Invoke-RestMethod -Headers $headers `
    -Uri 'http://127.0.0.1:8096/Environment/Drives'

$path = [uri]::EscapeDataString('F:\')
Invoke-RestMethod -Headers $headers `
    -Uri "http://127.0.0.1:8096/Environment/DirectoryContents?path=$path&includeFiles=false&includeDirectories=true"
```

The first response must include `F:\`; the second must return its directories
without an access error. An empty NFS export legitimately returns an empty list.
These responses, or the equivalent successful browse in Dashboard > Libraries,
prove Jellyfin can select the storage. Explorer visibility is not required.

For a repeatable check without creating a library or starting a scan:

```powershell
$credential = Get-Credential -Message 'Jellyfin administrator used only for this test'
.\packaging\windows\test-jellyfin-storage-browser.ps1 `
    -Credential $credential `
    -Path 'F:\', 'Z:\'
```

The harness fails unless Jellyfin reports every requested drive and can open
each root through the same API used by the folder picker.

Windows NFS is separate from SMB and requires the optional Client for NFS
features. Confirm them in an elevated PowerShell:

```powershell
Get-WindowsOptionalFeature -Online -FeatureName ServicesForNFS-ClientOnly
Get-WindowsOptionalFeature -Online -FeatureName ClientForNFS-Infrastructure
```

If needed, enable both and reboot when Windows requests it:

```powershell
Enable-WindowsOptionalFeature -Online -FeatureName ServicesForNFS-ClientOnly -All
Enable-WindowsOptionalFeature -Online -FeatureName ClientForNFS-Infrastructure -All
```

Remora calls only `%SystemRoot%\System32\mount.exe`; it never accepts another
`mount.exe` found earlier on `PATH`. Windows NFS targets must be drive roots:

```yaml
- type: nfs
  device: 192.168.1.20:/exports/media
  target: 'Z:\'
  options: mtype=hard,timeout=2,retry=2,sec=krb5
  probe-path: 'Z:\jellyfin'
  permission: rw
  heartbeat: 3
  failure-threshold: 3
```

Windows NFS usernames and passwords are rejected in YAML. Configure identity
mapping or Kerberos in Client for NFS and use supported `mount.exe` options.
Remora merges the native `mount.exe` table with MPR drive discovery, verifies
the configured server/export source, and then performs the same bounded I/O
probe as other required storage. Microsoft documents the supported syntax and
options in the [`mount` command reference](https://learn.microsoft.com/windows-server/administration/windows-commands/mount).

Test the Windows client independently before starting Remora:

```powershell
mount.exe -o anon,mtype=soft,timeout=2,retry=1 \\192.168.1.20\exports\media Z:
mount.exe
Get-ChildItem Z:\
umount.exe -f Z:
```

An anonymous mount commonly appears as UID/GID `-2`. If listing succeeds but an
`rw` probe returns access denied, fix the export ownership, anonymous identity
mapping, or server-side permissions; changing the Remora YAML cannot grant NFS
write access. Use `permission: r` only when the export is intentionally
read-only.
