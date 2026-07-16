# Jellyfin Remora

Jellyfin Remora is a companion supervisor for Jellyfin. The current macOS,
Windows, and native Linux alpha milestones support storage fencing, process
supervision, first-run setup, pre-start XML configuration reconciliation,
API-key provisioning, login watchdog checks, health checks, and local control.

Development milestones through the cross-platform stable release are tracked in [ROADMAP.md](ROADMAP.md).
The repeatable and real-fault high-availability coverage is recorded in [test/HA_TEST_MATRIX.md](test/HA_TEST_MATRIX.md).
Supervisor invariants and trust boundaries are defined in [docs/architecture-safety.md](docs/architecture-safety.md).
The local control-plane contract is documented in [docs/api-v1.md](docs/api-v1.md).
Build and review requirements are documented in [CONTRIBUTING.md](CONTRIBUTING.md).
Native bare-metal/systemd installation is documented in [docs/linux.md](docs/linux.md).

## Platform support

The macOS target is Apple Silicon (`arm64`) only. Intel (`x86_64`/`amd64`)
Macs are not supported, and there are no plans to add Intel macOS support or
publish Intel macOS artifacts.

## Platform adaptation test status

The status below records native platform and Jellyfin adaptation testing. A
passed adaptation test does not by itself mean that every stable-release gate,
such as long-running soak tests, signing, packaging, upgrade, and uninstall,
has passed.

| Platform | Architecture | Status | Tested scope or plan |
|---|---|---|---|
| macOS | Apple Silicon (`arm64`) | Passed（已通过） | Native clean-install and destructive HA tests with Jellyfin 10.10.7, 10.11.11, and 12.0.0 |
| macOS | Intel (`x86_64`/`amd64`) | Not planned（不计划） | No adaptation, testing, support, or release artifacts are planned |
| Windows 11 Pro | `amd64` | Passed（已通过） | Native service/task lifecycle, storage faults, reboot recovery, MSI lifecycle, and Jellyfin 10.11.11 |
| Windows Server 2022 | `amd64` | Passed（已通过） | Native service, SMB/NFS fault recovery, reboot, MSI lifecycle, and Jellyfin 10.11.11 |
| Windows Server 2025 | `amd64` | Passed（已通过） | Native service, SMB/NFS fault recovery, reboot, MSI lifecycle, and Jellyfin 10.11.11 |
| Windows | `arm64` | Not started（未开始） | Cross-build only; native dependencies and the Jellyfin compatibility matrix have not been tested |
| Linux (Debian 13 / Ubuntu 24.04 / Rocky Linux 10 / openSUSE Tumbleweed) | `amd64` | Passed（已通过） | Native systemd lifecycle, process adoption, physical/SMB/NFS fencing, filesystem/process faults, host reboot, and DEB/RPM lifecycle with Jellyfin 10.11.11 |
| Linux (Debian 13 / Ubuntu 24.04 / Fedora / openSUSE Tumbleweed) | `arm64` | Passed（已通过） | Native four-distribution ABI matrix plus real Jellyfin 10.11.11 systemd lifecycle, process adoption, physical identity, permission, read-only, full-disk, and process-hang faults on Ubuntu 24.04 ARM |

## Build

```sh
make build
```

Local builds always keep the daemon and control CLI together under a normalized
platform/architecture directory. `amd64` is named `x86_64` on disk:

```text
build/
├── darwin/arm64/
├── linux/arm64/
├── linux/x86_64/
├── windows/arm64/
└── windows/x86_64/
```

`make build` creates only the native leaf directory. `make cross-build` creates
all five supported or planned target directories. GitHub workflow layout is
unchanged for now.

Every `sample/*.yaml` platform template, including
[`sample/config-darwin.yaml`](sample/config-darwin.yaml) and
[`sample/config-windows.yaml`](sample/config-windows.yaml), and
[`sample/config-linux.yaml`](sample/config-linux.yaml), is embedded in
`remoractl` at build time. Release packages may retain the external files for
inspection, but init does not depend on them. `--sample-dir` explicitly
overrides the embedded platform template for development or customized builds.
For an installed pair of binaries, place `remoractl` and `jellyfin-remora` in
the same directory and run `remoractl init`. Init refuses to open the editor if
the sibling daemon is absent. It selects the embedded host template, edits and
strictly validates a `0600` temporary YAML copy, verifies every configured disk,
creates the four Jellyfin directories only beneath verified configured storage,
checks that each new directory is writable, and atomically writes
`remora-config.yaml` in the directory from which the command was invoked.

For each configured disk, init leaves an existing mount in place and performs
the requested real read or write/fsync/delete probe. A missing mount is mounted
by Remora and then probed; init never unmounts it. If an existing target is
mounted from a different source than the configured device, init warns and asks
whether to continue. Accepting that warning does not weaken runtime safety: the
daemon will still fence the mismatch until the mount or configuration is fixed.

On macOS, init generates a launchd plist beside `remora-config.yaml`; on Windows
it generates the Task Scheduler/Service PowerShell installer; on Linux it emits
a native systemd unit. With
administrator privileges init installs the platform definition idempotently and
asks before starting it. Without those privileges it keeps all generated files
in the current directory, prints a warning, and provides exact manual deployment
commands instead of failing. Automated Windows provisioning can still use
`--volume`, `--data-root`, and `--no-edit`; unresolved placeholders are rejected.
SysVinit is not supported.
When Remora runs as root, `jellyfin.run-as-user` is mandatory and Jellyfin is
started with that account.

For a zero-knowledge deployment, run `remoractl kickstart` instead. Its Bubble
Tea wizard detects native Jellyfin installations or validates a Generic
`.tar.gz`, `.tar.xz`, or `.zip`, creates a complete Jellyfin home, infers the
physical/SMB/NFS mounts containing all entered paths, offers real Jellyfin Web
language and region selections, and deploys the native service. Privileged
Linux Kickstart installs both Remora binaries atomically under `/usr/local/bin`
so systemd and SELinux receive a stable executable path. See
[`docs/kickstart.md`](docs/kickstart.md) for the workflow, generated defaults,
automation format, and real-platform validation matrix.

For a new Jellyfin data directory, configure `init.user` and `init.password`. Remora completes the setup wizard, handles the macOS package's OS-account bootstrap user on Jellyfin 10.11 and 12, renames it to the configured administrator, creates a `Jellyfin Remora` API key with mode `0600`, creates the optional login-watchdog user, and performs a controlled restart. Existing initialized servers are not sent through the setup wizard.

Configuration fields backed by a Jellyfin Web selection use the exact visible
option label, never its internal API value. For example, initial display
languages use `العربية`, `한국어`, or `Deutsch`, metadata languages use `Arabic`,
`Korean`, or `German`, and regions use `Saudi Arabia`, `Korea`, or `Germany`.
During first-run setup Remora reads the same public option catalogs as Jellyfin
Web, rejects labels absent from the installed server version, and preserves the
server default for omitted selections.

Configured `jellyfin.general`, `branding`, `playback.transcoding`, and
`networking` fields are reconciled into Jellyfin's XML immediately before each
start. Remora owns only fields explicitly present in YAML; omitted fields remain
owned by Jellyfin and dashboard changes to them are preserved. A YAML `null`
restores the corresponding Jellyfin default, while configured path values must
be absolute (or `default`). Existing XML files are backed up as
`*.remora.bak`, writes are atomic and preserve ownership/mode, and a multi-file
failure rolls back every file already changed. Remora never writes XML while the
setup wizard is incomplete. Custom CSS and splash images are validated before
any configuration file is changed.

The currently validated Jellyfin release lines are 10.11.x and 12.x. Real
clean-install and destructive HA runs have passed on macOS arm64 with Jellyfin
10.11.11 and 12.0.0; versioned API fixtures keep both response shapes under
test. Jellyfin data directories are not downgrade-compatible, so use a clean
data directory or a backup created by the target Jellyfin release when moving
from 12.x back to 10.11.x.

Run in the foreground during initial validation:

```sh
sudo ./build/darwin/arm64/remoractl init
./build/darwin/arm64/jellyfin-remora validate-config -c "$PWD/remora-config.yaml"
./build/darwin/arm64/jellyfin-remora -c "$PWD/remora-config.yaml"
./build/darwin/arm64/remoractl status
./build/darwin/arm64/remoractl healthcheck
```

`remoractl init` prepares missing Jellyfin directories automatically. The
standalone `validate-config --prepare` mode remains available for configurations
created without init.

Configuration schema v2 groups health settings by what they observe:

```yaml
remora:
  monitoring:
    interval: 1s
    jellyfin-api:
      interval: 10s
      failure-threshold: 3
    user-login:
      enabled: true
      interval: 60s
```

Each `disk` can independently set `failure-threshold` (default `1`). After the
disk has established one healthy baseline, Remora requires that many consecutive
fatal checks before fencing Jellyfin; a successful check resets the counter.
Startup still fails closed on the first unsafe check so a fresh daemon can never
start Jellyfin against unverified storage.

`remoractl status` and the converged results of lifecycle commands use
go-pretty Unicode-aware tables for process identity, Jellyfin server metadata,
storage, and active sessions. The active-sessions table is omitted when no
clients are active; otherwise its rows distinguish `playing`, `paused`, and
`idle` clients without exposing access tokens. Process start time, storage probe
latency, and the unique users currently playing or paused are included in the
status document. Use either `remoractl --json status` or
`remoractl status --json` for the additive, machine-readable `/v1/status`
document. Older clients safely ignore the new status fields.

`remoractl events [--limit 1..256]` displays the daemon's bounded state-transition
history; add `--json` for machine-readable output. Every `/v1` response exposes
an API version and operation ID in headers, while failures use stable structured
error codes. CLI exit codes distinguish usage errors, control-plane availability,
state conflicts, and operation timeouts.

The Phase 3 local control plane also provides:

```sh
remoractl logs remora --lines 200
remoractl logs jellyfin --lines 200
remoractl logs jellyfin -f
remoractl edit-config
remoractl apikey list
remoractl apikey create "Living Room"
remoractl apikey delete 01234567
remoractl session list
remoractl session stop a1b2c3d4
remoractl diagnose --output remora-diagnostics.json
```

Remora's own structured records are stored in `jellyfin-remora.log`. Jellyfin
stdout and stderr are never copied into that stream: they are preserved
verbatim in `jellyfin-console.log`, with independent lumberjack rotation under
the configured `remora.logs.path`. Rotated console files retain the
`jellyfin-console-*.log` naming pattern. `remoractl logs` defaults to `remora`;
the positional source and the older `--source` spelling are both supported.
`-f`/`--follow` streams appended bytes and follows rotation. ANSI sequences
emitted by Jellyfin are passed through unchanged. On macOS, Remora attaches a
raw pseudo-terminal to Jellyfin so its console formatter sees a real TTY while
the process remains in its independently managed process group. Remora also
defaults `TERM=xterm-256color` and
`DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=1` when the operator has not
explicitly set those variables. Windows currently preserves ANSI bytes emitted
by Jellyfin but does not yet allocate a ConPTY console.

API-key output contains only SHA-256-derived identifiers, never access tokens;
the active Remora key cannot be revoked through its own control API. Configuration
editing verifies the daemon-observed checksum, validates the edited YAML, rejects
symlinks and concurrent replacement, then performs an owner-only atomic write.
Diagnostic bundles contain build/status/event data and bounded Remora log output,
not configuration contents or stored credentials. This single-host project does
not run a Prometheus or other network metrics exporter.

### macOS tarball installations

Remora also supports the portable macOS Jellyfin archive layout, where the
lowercase `jellyfin` executable and `jellyfin-web` directory are siblings. Set
`jellyfin.path` to the extracted directory and `jellyfin.web-dir` to its
`jellyfin-web` child. Preserve the executable bit when extracting. On current
macOS releases, a downloaded, unsigned archive may additionally require local
ad-hoc signing after its checksum has been verified:

```sh
xattr -dr com.apple.quarantine /absolute/path/to/jellyfin-10.10.7
codesign --force --deep --sign - /absolute/path/to/jellyfin-10.10.7/jellyfin
```

On Darwin, `validate-config` reports this attribute and the daemon emits a WARN
record with the executable path before supervision begins. Detection is
advisory: Remora does not remove metadata, bypass Gatekeeper, or refuse startup.

Command-line parameters are Jellyfin-version-specific. Jellyfin 10.10.7 hosts
the configured `--webdir` by default and rejects `--hostwebclient`; a compatible
optional parameter is `package-name: jellyfin-remora-tar`.

The REST listener accepts loopback addresses only. On Unix, the daemon defaults
to `/tmp/.s.remora.<restapi.port>` and `remoractl` discovers that socket under
`/tmp` automatically. Use `--socket`/`-s` to select among multiple non-default
instances, or `--host http://127.0.0.1:8095` as a fallback.

## NFS locking on macOS

For media-only NFS shares, remote file locking usually adds no value: Jellyfin
opens media files for streaming rather than coordinating cross-client writes.
On Darwin, Remora therefore adds macOS's `nolocks` option by default to NFSv2/v3
mounts when `disk.options` does not already select a locking policy. This also
avoids the separate NFSv3 status and lock-manager RPC dependency. A typical
media-share entry is:

```yaml
disk:
  - type: nfs
    device: 192.168.1.109:/media
    target: /Volumes/jellyfin-media
    probe-path: /Volumes/jellyfin-media/remora-probe
    permission: rw
    options: vers=3,resvport,nolocks,rsize=65536,wsize=65536,intr,soft
```

If `options` is omitted, Darwin uses the recommended media-share defaults
`vers=3,resvport,nolocks,rsize=65536,wsize=65536,intr,soft`. If custom NFSv2/v3
options omit a locking choice, Remora appends `nolocks`; all other explicit
values are preserved. Remora does not add `nolocks` to an explicit NFSv4 version
because macOS defines that option only for NFSv2/v3.

Do not apply the media-share default blindly to Jellyfin's data, configuration,
metadata, or database directories when multiple clients may write them. Use
NFSv4, which integrates locking into the protocol, or explicitly request remote
locking for NFSv3 in that case.

With NFSv3, macOS remote locking requires the server to provide `rpc.statd` and
the Network Lock Manager. If the server is missing `rpc.statd`, `mount_nfs` fails
with an error similar to:

```text
can't mount with remote locks when server is not running rpc.statd
```

On a Rocky Linux or RHEL NFS server, install the NFS utilities and enable the
NFSv3 status service together with the server:

```sh
sudo dnf install nfs-utils
sudo systemctl enable --now rpc-statd nfs-server
rpcinfo -p | grep -E 'status|nlockmgr'
```

The final command should list RPC program `100024` (`status`/`rpc.statd`) and
`100021` (`nlockmgr`). When a firewall separates the client and server, pin and
allow the `mountd`, `statd`, and `lockd` ports in `/etc/nfs.conf`; NFSv4 normally
needs only TCP port 2049. See the
[RHEL NFS firewall guidance](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/9/html/securing_networks/securing-network-services_securing-networks).

For media-only shares, `nolocks` is the recommended production mode and avoids
unnecessary lock traffic. `locallocks` is different: it creates locks visible
only to the current Mac and can mislead applications into assuming other clients
are coordinated. For shared writable Jellyfin application data, start
`rpc.statd` for NFSv3 or use NFSv4 instead.

## launchd

Run init with `sudo` to validate storage, install the generated plist at
`/Library/LaunchDaemons/io.github.chowdpa02k.jellyfin-remora.plist`, and choose
whether to bootstrap or restart the service:

```sh
sudo /absolute/path/to/remoractl init
```

The generated plist contains the absolute sibling `jellyfin-remora` path and
the absolute `$PWD/remora-config.yaml` path. Repeating init safely replaces the
installed definition; if startup is confirmed for an already loaded service,
init performs `bootout` followed by `bootstrap` so launchd reads the new plist.
A non-root run generates both files locally and prints the equivalent `cp`,
`chown`, `chmod`, `bootout`, and `bootstrap` commands.

SMB passwords in YAML are supported for the first milestone but can be visible transiently to privileged process inspection. Keep the configuration `0600`; Keychain-backed credentials are planned for a later milestone.

## Safety rules

- A missing mount, wrong mount source, or failed required I/O probe fences Jellyfin.
- A reachable mount with working I/O is only degraded when its SMB/NFS port probe fails.
- Storage must pass three consecutive checks before automatic recovery.
- Manual stop always overrides automatic recovery.
- Five process failures in ten minutes open the restart circuit; `remoractl start` resets it.
- Transient Darwin `U` waits during heavy library scans do not degrade an otherwise healthy server; a continuously uninterruptible process still reaches forced recovery after `server-stop-timeout`.

## Development checks

```sh
make build
make test
make check
make vuln
make cross-build
./test/package_linux_tar.sh
LINUX_TEST_ARCH=arm64 ./test/linux_container_matrix.sh
```

CI runs the Linux backend matrix on Debian 13, Ubuntu 24.04, Fedora, and
openSUSE Tumbleweed on matching native amd64 and arm64 GitHub-hosted kernels.
It also verifies reproducible amd64/arm64 DEB and RPM output, and runs a real
Jellyfin 10.11.11 destructive systemd/storage gate on Ubuntu 24.04 ARM. The
additional physical-host and network-storage evidence is recorded separately
in the HA matrix.

The module requires the patched Go toolchain declared in `go.mod`. With the default
`GOTOOLCHAIN=auto`, Go downloads that toolchain when the locally installed Go command
is older. Install the vulnerability scanner with:

```sh
go install golang.org/x/vuln/cmd/govulncheck@latest
```

`make build` injects release metadata into both commands. Override `VERSION`, `COMMIT`,
or `BUILD_DATE` for release builds, and inspect the result with
`jellyfin-remora --version` or `remoractl --version`.

Configuration is decoded through a versioned, in-memory migration pipeline before
strict validation. Unversioned and version 1 files are migrated to version 2 without
rewriting the source file; old heartbeat multipliers are converted to explicit
durations, and conflicting legacy/current keys fail closed.

## License

Jellyfin Remora is available under the [MIT License](LICENSE).
