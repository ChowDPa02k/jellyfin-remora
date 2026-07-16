# Jellyfin Remora development roadmap

This roadmap takes the project from the current Apple Silicon macOS prototype to a supported, reproducible release on macOS arm64, Linux, and Windows. Intel macOS is explicitly out of scope and is not planned. Versions are milestone labels rather than promised dates. A phase is complete only when its exit gate passes.

## Release principles

- Jellyfin always runs as a separate OS process; Remora must not replace or strip its hardware/runtime environment.
- Storage safety overrides availability. A missing, mismatched, or non-writable required path fences Jellyfin.
- Every state-changing operation is serialized through the supervisor state machine.
- Platform-specific code stays behind shared process, storage, service, secret-store, and IPC interfaces.
- Configuration and state formats are versioned before the first beta; incompatible changes require migration tests.
- A platform is “supported” only after native installation, upgrade, removal, fault-injection, and long-running tests pass.

## Phase 0 — Repository and engineering baseline (`v0.1.0-alpha.1`)

Current implementation already provides the two Go binaries, Darwin process supervision, physical/SMB/NFS checks, storage fencing, loopback REST/Unix-socket control, basic CLI commands, log rotation, and platform interfaces.

Completed with the first real configuration iteration:

- Config schema v2, strict unknown-key rejection, legacy heartbeat aliases, duration-preserving v1 migration, and checked-in JSON Schema.
- Non-mutating `validate-config`, protected `--prepare`, credential-file permission checks, and isolated timed I/O probes.
- APFS plus SMB validation with Unicode share names, URL-encoded mount sources, and Bonjour service-name/IP equivalence.
- Apple Silicon smoke test against Jellyfin 12.0.0: first-run database creation, hardware capability discovery, `/health` transition to `RUNNING`, and graceful stop all passed.
- Clean-directory setup against Jellyfin 12.0.0: OS-account bootstrap-user handling, configured administrator rename, setup completion, owner-only API-key persistence, watchdog-user creation, controlled restart, and persistent-session login checks all passed.
- Normal-restart regression coverage confirms Jellyfin 12's transient setup-listener state is ignored until core health and a stable incomplete-wizard streak agree; existing installations no longer flash into `FIRST_START`.
- Destructive local fault test using `test/test.yaml`: write-permission loss fenced and stopped Jellyfin; restoring permission required the configured recovery streak and launched exactly one replacement process. Explicit restart and manual stop also passed.
- The repository now covers 111 top-level tests plus real Jellyfin fault injection: crash-loop and first-start initialization circuit breaking, Remora crash adoption with original uptime, exact argv identity, duplicate-instance locking, API-key revocation recovery, sticky watchdog degradation, process-group cleanup, stale PID rejection, live SMB unmount fencing/recovery, per-disk consecutive-failure thresholds, transient and sustained `D`/`U` handling, fail-closed XML reconciliation, stale-health isolation, Darwin provenance warnings, validated configuration initialization, Jellyfin Web selection-label resolution, bounded control events, management API compatibility, and Unicode-safe status/session rendering. See `test/HA_TEST_MATRIX.md`.
- Semantic build metadata (`version`, commit, build date, Go version, target) is injected into both commands.
- GitHub Actions definitions cover formatting, unit/race tests, vet, native macOS/Windows tests, all target cross-builds, dependency review, vulnerability scanning, and launchd plist validation.
- The module requires a patched Go toolchain and the local `govulncheck` gate reports no reachable vulnerabilities.
- A fail-closed, version-step configuration migration pipeline upgrades legacy unversioned input in memory without rewriting operator files.
- State-machine invariants, filesystem trust boundaries, child-process privileges, and the current secrets threat model are documented as change-review requirements.
- The project is licensed under MIT and prepared for the public `github.com/ChowDPa02K/jellyfin-remora` repository.

Remaining work:

- Add contribution, security-reporting, and support-policy documents.

Exit gate:

- A clean checkout builds both commands on all target GOOS values; CI is mandatory and branch protection requires it.
- Tests cover every state transition and no test leaves processes, sockets, mounts, or temporary credentials behind.
- `go test -race ./...`, `go vet ./...`, vulnerability scanning, and cross-builds all pass.

## Phase 1 — Production-grade Darwin supervisor (`v0.2.0-alpha`)

- Replace text parsing where practical with stable Darwin system APIs for mount/process discovery; cover spaces, Unicode, aliases, volume renames, and multiple Jellyfin instances.
- Extend current executable/argument adoption with UID, durable start identity, and stronger PID-reuse protection.
- Handle sleep/wake, network changes, volume arrival/removal, shutdown, launchd restart, and Remora crashes without creating duplicate Jellyfin processes.
- Add Keychain-backed SMB credentials. Keep inline YAML passwords only as an explicitly insecure compatibility mode with redaction tests.
- Add mount retry limits, jittered restart backoff, and explicit administrative un-fence/reset operations. Per-disk consecutive-failure and global recovery thresholds are implemented.
- Build `install`, `uninstall`, and extended `diagnose` workflows; validated `remoractl init`, `validate-config`, protected directory preparation, and Darwin launchd-plist generation are already available.
- Test Apple Silicon builds against Jellyfin 10.11.x and the current 12.x-compatible API surface.
- Real Darwin NFSv4 loss and recovery passed against `192.168.1.109:/data`: the writable probe established a healthy baseline, unmount fenced and stopped the old Jellyfin PID, and remount completed the configured recovery streak before starting exactly one replacement PID. NFSv3 server-side `rpc.statd` requirements, the media-oriented `nolocks` default, and explicit locking guidance for shared application data are documented.

Exit gate:

- Real APFS/removable-volume, SMB, and NFS fault tests demonstrate: disconnect causes fencing, no false local data tree is created, the old process exits, and recovery requires the configured healthy streak.
- A 7-day soak test covers media playback/transcoding, sleep/wake, network interruption, Remora restarts, log rotation, and repeated Jellyfin crashes.
- Signed development artifacts install, upgrade, and uninstall cleanly through launchd on arm64 macOS.

## Phase 2 — Jellyfin lifecycle and configuration management (`v0.3.0-alpha.8`)

Completed in the `test/test.yaml` iterations:

- Typed Jellyfin API client foundations with public version/setup discovery, structured API errors, Jellyfin authorization headers, and `httptest` contract coverage.
- First-run detection and complete setup sequence: initial configuration, Jellyfin 12 bootstrap user, remote access, completion, configured administrator rename/login, and Remora API-key creation.
- Atomic API-key persistence with owner-only permissions and idempotent reuse of an existing `Jellyfin Remora` key.
- Optional login watchdog that creates a missing configured user, retains one real session for periodic `/Users/Me` checks, and reauthenticates only after Jellyfin rejects or revokes its token.
- Versioned public-info, setup-user, authentication-result, and API-key contract fixtures for Jellyfin 10.11.x and 12.x; revoked API-key detection and automatic replacement.
- Typed, atomic, backup-first XML reconciliation for supported general, branding, playback/transcoding, and networking fields before every process start.
- Explicit ownership precedence: configured Remora fields win before startup, while unspecified fields and dashboard-managed data remain byte-for-byte untouched.
- Setup-wizard write suppression, custom CSS/image validation before the first write, XML mode/ownership preservation, idempotence, and multi-file rollback on failure.
- Real Jellyfin 12.0.0 validation using `test/test.yaml`: configured performance and branding values were applied before start, `/health` reached `RUNNING`, one-instance restart passed, and graceful stop preserved the values.
- Real Jellyfin 10.11.11 clean-install and destructive HA validation using the same configuration: setup/API-key/watchdog provisioning, XML reconciliation, key revocation recovery, crash replacement/circuit reset, Remora adoption, duplicate locking, storage fencing, manual-stop precedence, and live SMB disappearance/recovery all passed.
- Real Jellyfin 10.10.7 arm64 tarball validation: lowercase executable and sibling Web UI discovery, clean setup, full XML reconciliation, watchdog/API-key provisioning, key revocation recovery, PID replacement, Remora crash adoption, writable-path fencing/recovery, and live SMB disappearance/recovery all passed.
- Replacement processes clear the preceding PID's health sample before entering `STARTING`, preventing a stale healthy result from erasing crash history before the new PID is checked.
- `remoractl` renders go-pretty-backed Unicode-safe process/storage/session tables by default while retaining additive JSON output; status now includes the run-as UID, Jellyfin version/server name, and normalized active sessions for both supported server lines, and omits the sessions table when no client is active.
- Configuration schema v2 replaces mixed heartbeat multipliers with explicit `monitoring.jellyfin-api` and `monitoring.user-login` intervals, preserves v1 timing through in-memory migration, and adds independently debounced disk failure thresholds.
- `remoractl init` requires sibling binaries, validates an edited platform
  template and its real mount/read/write behavior, then atomically writes
  `$PWD/remora-config.yaml`. It generates platform service definitions beside
  the configuration, installs them idempotently when privileged, asks before
  startup, and otherwise prints exact manual deployment instructions. Darwin
  uses launchd, Windows uses Task Scheduler with native-service compatibility,
  and the systemd path activates with the Phase 5 Linux backend and sample.
- Setup selection fields accept the exact labels shown by the installed Jellyfin Web UI instead of internal API codes. Remora resolves the server-provided catalogs at setup time, preserves omitted defaults, and fails closed on labels unsupported by that Jellyfin version.
- First-start API failures now use bounded exponential backoff; five consecutive failures stop the incomplete server and open the administrative-reset circuit instead of retrying setup every supervisor tick.
- Runtime health readiness and failure accounting share one sample per tick, Darwin adoption uses exact kernel argv boundaries and the discovered process age, and `remoractl --host localhost` pins the validated loopback address.

Exit gate:

- Clean-directory tests automatically initialize Jellyfin, create an API key, restart, and become healthy without manual web setup.
- Existing-server tests prove unspecified settings and user-managed data remain unchanged.
- Contract tests pass against supported 10.11.x and 12.x server fixtures.

Phase 2 exit gate passed on macOS arm64 on 2026-07-13. Intel macOS is not a
supported or planned target.

## Phase 3 — Complete control plane and observability (`v0.4.0-alpha`)

Completed across `v0.4.0-alpha.1` through `v0.4.0-alpha.6`:

- Documented the additive local `/v1` contract, status enums, compatibility rules, response-version metadata, per-request operation IDs, and structured stable error codes.
- Retained the status response shape used by older clients while extending it with process start time, storage-probe latency, and sorted playing-user summaries; CPU/RSS, listening endpoints, and active session details remain available.
- Normalized local build outputs as `build/<platform>/<arm64|x86_64>/`, with sibling daemon/CLI binaries in every leaf so packaged `remoractl init` always satisfies its executable-discovery contract.
- Embedded every `sample/*.yaml` platform template in `remoractl`, retaining `--sample-dir` only as an explicit override so binary-only deployments can initialize from any working directory.
- Made init prepare the four Jellyfin data/config/cache/log directories automatically after storage verification, reject paths outside configured storage or through symlink escapes, and require a successful writable probe before writing the final configuration.
- Simplified terminal status output by removing the composite storage-latency column, coloring state/health only on ANSI-capable terminals, and rendering storage targets without truncation.
- Embedded and printed the Jellyfin Remora ASCII splash once after configuration validation and exclusive-instance acquisition during daemon startup.
- Added a 256-entry in-memory state-transition history through `GET /v1/events` and `remoractl events`, with bounded result selection and table/JSON rendering.
- Defined deterministic `remoractl` exit codes for usage, local failures, daemon/API availability, state conflicts, and operation timeouts.
- Preserved local-only control over owner-restricted Unix sockets or loopback REST and serialized lifecycle and Jellyfin-management mutations inside the supervisor boundary.
- Completed source-selectable `remoractl logs remora|jellyfin` with rotation-aware `-f`, `edit-config`, `apikey list/create/delete`, and `session list/stop`; Jellyfin console output is captured verbatim outside Remora's structured stream, macOS uses a raw PTY to retain formatter colors, and both log families use independent lumberjack rotation.
- Added Darwin Jellyfin-process-tree ffmpeg accounting, active-transcode session summaries, and owner-only structured local diagnostic bundles.
- Added concurrent-client operation-ID, real slow-header timeout, request cancellation, malformed/oversized request, socket permission, daemon restart, log trust-boundary, and old-client/new-daemon compatibility coverage.
- Kept transient Darwin `U` waits in `RUNNING` when storage and `/health` remain healthy, preventing initial library scans from flapping status while retaining forced recovery for a continuously uninterruptible process beyond the stop timeout.
- Keep remote control disabled by default. If non-loopback control is later enabled, require TLS and scoped authentication rather than reusing Jellyfin credentials.

Exit gate:

- Every daemon-facing CLI action has a matching documented API operation and deterministic exit codes.
- API compatibility tests demonstrate that an older supported `remoractl` can control a newer daemon within the same major version.

Phase 3 exit gate passed on macOS arm64 on 2026-07-14. Platform-native process
accounting is implemented for Darwin and Windows; Linux remains part of Phase 5.
The shared API and CLI contract cross-builds for all declared targets.

## Phase 4 — Windows support (`v0.6.0-alpha`)

Windows precedes native Linux support because Linux users are more likely to run
Jellyfin through Docker, while Windows users need a native supervisor. Windows
Server is the primary compatibility target for native Jellyfin deployments;
Windows 11 Pro provides the desktop/workstation baseline. The first deliverable
is therefore a Windows-safe storage configuration and probe backend; process and
service integration build on that storage boundary.

Implemented on the initial Windows development host:

- Strict Windows configuration validation, volume-GUID/label/filesystem identity,
  separate writable probe paths, native volume resolution, and fail-closed target
  verification are covered by unit and live NTFS tests.
- `remoractl init` enumerates local fixed/removable volumes through Win32 APIs,
  displays mount paths, labels, filesystems, total/free capacity and GUIDs, and
  supports interactive selection or unattended `--volume D:\` preparation
  without requiring the operator to copy a GUID. A guarded clean-init harness
  also proves `--no-edit`/`--data-root`, rejects unresolved placeholders, compares
  the generated identity with `mountvol`, runs `validate-config`, and cleans up.
- Directory initialization resolves reparse points and the nearest existing
  ancestor through `GetVolumePathNameW` before and after creation. A live junction
  fixture proves a syntactically in-tree path that escapes to another volume is
  rejected before it can become a Jellyfin data tree.
- SMB drive discovery and reconnect use MPR APIs. When requested, the Windows
  backend reads a Generic Credential from the exact service identity with
  `CredReadW`, passes it to `WNetAddConnection2W`, and clears the in-memory
  password buffer after use. Live validation covers the configured Unicode UNC
  even when the service token cannot see the Explorer mapping. A temporary
  unused drive-letter fixture proves connect, write/flush/delete, forced
  disconnect, reconnect, and cleanup without disturbing the operator's existing
  mapping. Windows rejects SMB `user`/`password` fields in YAML.
- Windows NFS uses only the optional system Client for NFS `mount.exe`, rejects
  plaintext YAML credentials, merges the native NFS mount table with MPR drive
  discovery, and validates the configured server/export plus bounded I/O. The
  installed system client proves an unreachable export fails within the
  configured context deadline and leaves no drive mapping; a live export proves
  mount, read probe, forced unmount, and reconnect.
- Local control uses an ACL-protected named pipe, duplicate daemon instances use
  a Windows file lock, and process adoption uses exact executable/argument
  matching.
- Supported-platform decisions are isolated in build-tagged files: signal
  handling, init sample/binary naming, executable mode/candidates, default web
  assets, storage, IPC, service, ACL, and process behavior no longer accumulate
  `runtime.GOOS` branches in shared lifecycle code.
- Jellyfin process trees are assigned to a kill-on-close Job Object; CPU/RSS and
  descendant ffmpeg accounting are native, API shutdown is preferred for graceful
  stop, and Job termination is the forced fallback. Native process-manager tests
  cover exact adoption, duplicate-match rejection, stale-PID non-interference,
  and descendant cleanup.
- IPv4/IPv6 listening ports are read by owning PID through the native IP Helper
  API instead of being guessed from configuration. Synthetic table tests, a real
  listener, and Jellyfin 10.11.11 prove port `8096` appears only while the process
  is actually listening and is cleared after stop.
- The daemon has an SCM service handler and `remoractl init` emits an
  administrator-reviewed PowerShell service installer. Native tests, real
  Jellyfin 10.11.11 start/restart/stop/adoption, amd64 builds, and Windows arm64
  cross-builds pass. The installer grants `SeServiceLogonRight` through the LSA
  API when a custom service credential is supplied, so a clean host does not
  require a manual `secpol.msc` step.
- The generated installer also retains an interactive/highest Task Scheduler
  deployment for workstations. It prevents coexistence with the SCM service,
  stops the running task before unregistering it, and has a disposable Win11
  harness covering principal SID, action process, named-pipe status, a real
  Jellyfin PID, and process cleanup.
- A real SCM run under `NT SERVICE\JellyfinRemora` verifies the service identity,
  dynamic named-pipe ACL, physical-volume access, and fail-closed rejection of
  the interactive user's unavailable SMB credential. Service lifecycle and
  daemon warning/error records are written to the Application event log.
- A repeatable elevated account-change test starts the native service under its
  virtual account, reads status through the named pipe, stops it, changes SCM to
  `LocalService`, reapplies ACLs, starts it again, proves the daemon identity
  changed, and removes all service/test artifacts.
- A two-stage reboot harness records an already healthy automatic service's boot
  time, SCM/Jellyfin process identity, service account, named-pipe status, and
  storage baseline. Post-boot verification requires new processes and every
  configured storage result healthy; it deliberately never initiates a reboot.
  The harness passes on a clean Windows 11 VM with a local NTFS volume and live
  NFS export.
- The Windows packaging script produces reproducible unsigned development ZIPs,
  checksums, manifests, optional WiX MSI output, and an explicit Authenticode
  path; two independent fixed-epoch builds produce byte-identical ZIP hashes,
  and CI never labels unsigned artifacts as release-signed. A temporary
  Code Signing certificate proves SDK discovery, private-key/EKU preflight,
  RFC3161 signing and verification for both executables and the MSI, and that
  the ZIP contains the exact signed executable bytes. This development evidence
  does not replace the release-certificate validation deferred to `v1.0.0`.
- Local and clean-VM WiX validation covers MSI install, repair, injected
  transactional rollback with old-version restoration, major upgrade, downgrade
  blocking, and uninstall with complete Program Files cleanup; the lifecycle is
  captured in `packaging/windows/test-msi.ps1` for disposable elevated hosts.
- CI pins explicit Windows Server 2022 and 2025 runners as the primary Windows
  compatibility matrix for native tests, clean-volume initialization, and the
  complete unsigned MSI lifecycle instead of relying on the moving
  `windows-latest` label.

Windows 11 Pro 23H2 build 22631 is the desktop compatibility baseline and its VM
evidence recorded on 2026-07-14 replaces a separate Windows 10 run. It covers
clean init without third-party tools; a temporary NTFS VHD wrong-volume/reused-letter,
missing target, reassignment, read-only, zero-free-space, detach, and recovery
matrix; live Generic-Credential SMB connect/write/disconnect/reconnect and
credential deletion/fail-closed/restoration recovery under a password-backed
service identity; live NFS mount/unmount/reconnect and firewall-induced
`DEGRADED -> STORAGE_FENCED -> RUNNING` recovery with a new Jellyfin PID; custom
service-account installation, named-pipe status, automatic reboot recovery,
Task Scheduler install/uninstall with a real Jellyfin PID, real Jellyfin 10.11.11
on observed port 8096, and the complete unsigned MSI lifecycle. A native hung
health-endpoint test proves bounded checks, forced Job Object cleanup, and
restart with a new PID. The full Go test suite, vet, Windows arm64 build, and
Darwin/Linux amd64/arm64 cross-builds pass after these changes.

The repository-root `config.yaml` also passes native validation on the Windows
development host with its real D: physical volume, Unicode SMB source at F:,
and four Jellyfin data paths. A complete foreground lifecycle with Jellyfin
10.11.11 observes PID and port 8096, all six storage checks healthy, and the
strict transition sequence `PREFLIGHT -> STOPPED -> STARTING -> RUNNING ->
STOPPING -> STOPPED`. A real setup-listener race found during this run is now
covered: `/health` alone cannot report RUNNING until `/System/Info/Public` also
succeeds, preventing an early green state followed by a STARTING regression.

Service-session visibility is validated separately from Explorer. In a Win11
desktop session, Explorer and a non-elevated interactive task correctly do not
see the service-created SMB T: or NFS Z: mappings. While those mappings remain
absent from Session 1, Jellyfin 10.11.11 in service Session 0 returns C:, T:, and
Z: from `/Environment/Drives`; its directory-browser API lists the real Unicode
SMB contents at T: and successfully opens the empty NFS root at Z:. This proves
the Dashboard media-library picker can use both mappings without requiring them
to appear in Explorer.

Windows Server 2022 Datacenter build 20348 passed the clean-host amd64 matrix on
2026-07-14. Evidence covers clean volume discovery and initialization without
third-party disk tools; offline native `go test ./...` and `go vet ./...`; live
NTFS identity failures on a disposable VHDX; service installation and account
change under Windows PowerShell 5.1; named-pipe status; Application event log
registration; NFS session isolation, fencing, and recovery; and automatic
service recovery across reboot. A password-backed local service identity read a
Generic Credential Manager entry, connected the real Unicode SMB share in
Session 0, passed write/flush/delete probes, stayed fenced while TCP 445 was
blocked, and reconnected after restoration. Real Jellyfin 10.11.11 completed
first-run initialization under Remora and reported port 8096. Its own
`/Environment/Drives` API returned C:, F:, and Z:, and its directory-browser API
listed 14 directories on the Unicode SMB root and opened the empty NFS root. A
final reboot changed both the SCM and real Jellyfin PIDs while both browser
checks still passed, proving that Explorer visibility is not required for
service-owned storage. The unsigned MSI install, repair, injected rollback,
major upgrade, downgrade rejection, and uninstall transactions all produced
their expected Windows Installer exit codes and final filesystem state. When
invoked through OpenSSH the wrapper channel remained attached after the
completed MSI transactions, so the transaction logs and postconditions, rather
than SSH channel closure, are the recorded evidence.

Windows Server 2025 Datacenter build 26100 passed the clean-host amd64 matrix on
2026-07-15. Native `go test ./...` and `go vet ./...` exposed and then verified a
Windows log-follow rotation fix that opens logs with delete sharing. Clean init,
a disposable NTFS VHDX identity matrix, the service-account change harness, and
live SMB/NFS disconnect-reconnect tests passed. A password-backed custom service
identity mounted the Unicode SMB share and NFS export, fenced real Jellyfin while
both servers were blocked, and returned to `RUNNING` with a new Jellyfin PID after
connectivity was restored. Jellyfin 10.11.11 completed first-run initialization;
its storage browser returned C:, F:, and Z: and opened both network roots. An
automatic reboot changed the SCM and Jellyfin PIDs while every storage probe and
both browser checks remained healthy. The unsigned MSI install, repair, injected
rollback, upgrade, downgrade rejection, and uninstall matrix passed with the
expected exit codes and no remaining Program Files directory. The run also found
that the portable Windows sample must not assume a D: transcode directory, so it
now retains Jellyfin's default transcode path.

The Phase 4 exit gate passed on 2026-07-15. Windows Server 2022, Windows Server
2025, and Windows 11 Pro are complete; the Windows 11 Pro run satisfies the
desktop-client requirement, so a separate Windows 10 run is not required.
Release-certificate Authenticode signing and verification are deferred to the
`v1.0.0` release gate. Windows arm64 remains build-only and is not a released
target until its complete native dependency and Jellyfin matrix passes.

Exit gate:

- Windows 11 Pro passes the desktop/workstation amd64 matrix; its recorded clean-VM evidence satisfies the client baseline without a duplicate Windows 10 run.
- Windows Server 2022 passes the primary amd64 compatibility matrix, including native service lifecycle, clean initialization, storage fault recovery, reboot, upgrade/rollback, and MSI uninstall behavior.
- Windows Server 2025 passes the same primary amd64 compatibility matrix; arm64 is released only if the complete native dependency and Jellyfin test matrix passes.
- A clean Windows installation can produce a valid physical-volume configuration through `remoractl init` without third-party tools, registry browsing, or prior knowledge of volume GUIDs; the documented `mountvol` and PowerShell procedures produce the same identity.
- Physical-volume identity tests prove that a reused drive letter cannot make the wrong disk healthy and that a configured volume remains identifiable across drive-letter changes.
- Reboot, service-account change, SMB credential expiry, share disconnect, NFS mount loss where supported, drive-letter change, hung process, forced stop, and upgrade tests pass.

## Phase 5 — Native Linux support (`v0.8.0-alpha`)

Native Linux support follows Windows because Linux operators commonly deploy
Jellyfin through Docker. This phase adds a supported bare-metal/systemd path for
operators who need Remora to own the host process and storage fence directly.

Phase 5 exit gate passed on 2026-07-16. The
`/proc`/pidfd/process-group/subreaper backend, mountinfo/statfs storage identity,
cgroup-v2 accounting, physical/SMB/NFS
mounting, file/libsecret credential providers, systemd integration, portable
tarballs, native DEB/RPM builders, artifact checksums, and continuous
amd64/arm64 distribution/package matrices are implemented. The arm64 ABI matrix
runs on a native GitHub-hosted arm64 kernel rather than user-mode QEMU, and the
destructive native checks are preserved as repeatable `test/linux_real_*.sh`
harnesses. Real Jellyfin 10.11.11 runs on Debian 13 amd64 and Rocky Linux 10
amd64 have covered process adoption, physical/SMB/NFS fencing, permission loss,
read-only and full filesystems, hung-process recovery, service restart, storage-
server and client reboots, and native-package install/upgrade/rollback/removal.
The same real Jellyfin release and packaged `0.8.0-alpha.8` binaries pass the
repeatable native-systemd matrix on Ubuntu 24.04 and openSUSE Tumbleweed,
including process adoption, physical identity, permission, read-only, full-disk,
and stopped-process faults. A native Ubuntu 24.04 ARM runner additionally passes
the official Jellyfin 10.11.11 arm64 package, systemd lifecycle, Remora crash
adoption, wrong-filesystem identity, permission loss, read-only and full-disk
fencing, and hung-process recovery. SMB/NFS disconnect and host-reboot gates
remain the physical Debian/Rocky amd64 evidence; the exit gate does not require
repeating every architecture-independent transition on every architecture.

`v0.8.0-alpha.9` adds the Bubble Tea `remoractl kickstart` zero-knowledge
deployment path. It detects native or Generic Jellyfin installations, validates
archive OS/architecture and extraction safety, infers physical/SMB/NFS storage,
creates a complete Jellyfin home, offers embedded real-API localization labels,
emits a minimal configuration, and installs the native service idempotently.
The gate passed with Jellyfin 12.0.0 and Generic 10.10.7 on macOS arm64,
RPM 10.11.11 on SELinux-enforcing Rocky Linux 10, and DEB 10.11.11 with live
NFS+CIFS mounts on Debian 13. Linux native test binaries were also executed on
Debian, including the Linux-only systemd/storage/process code paths.

- Implement the Linux platform backend using `/proc`, pidfd where available, process groups, child-subreeper behavior, mountinfo/statfs, and cgroup awareness without imposing CPU/GPU limits.
- Support physical filesystems, `mount.cifs`, and NFS; add libsecret/file-based credential-provider interfaces and protocol-specific timeout guidance.
- Provide systemd units with correct foreground behavior, runtime/state directories, privilege separation, restart limits, shutdown ordering, and network/mount dependencies.
- Support existing-process adoption, zombie/uninterruptible-state detection, descendant cleanup, and ffmpeg accounting on cgroup v2 and non-cgroup systems.
- Package portable tarballs first, then `.deb` and `.rpm`; provide install, upgrade, rollback, and purge tests in clean VMs/containers.

Exit gate:

- Integration matrices pass on representative current Debian/Ubuntu, Fedora/RHEL-family, and one rolling distribution, on amd64 and arm64.
- SMB/NFS disconnect, read-only remount, stale mount, full disk, permission loss, process hang, systemd restart, and host reboot tests pass.

## Phase 6 — Cross-platform beta hardening (`v0.9.x-beta`)

- [x] Freeze configuration schema v2, state-file compatibility, REST API v1, CLI exit codes, service names, filesystem locations, and upgrade rules. `v0.9.0-beta.1` establishes a machine-readable manifest, shared code constants, compatibility policy, and drift tests.
- `v0.9.0-beta.2` adds an additive database-safety contract: Remora never opens live SQLite files, confirms new corruption log evidence through Jellyfin APIs, durably fences `DATABASE_DAMAGED`, and requires an explicit repaired `start` acknowledgement.
- Add property/state-machine tests, fuzzing for YAML/API/state parsing, failure injection for every syscall boundary, and restart-during-operation tests.
- Run 30-day soak tests per OS with playback and hardware transcoding; record Remora CPU, memory, file-descriptor/handle growth, and restart behavior.
- Complete secret-store migration, least-privilege reviews, dependency/license audits, SBOM generation, vulnerability response procedures, and external security review.
- Write operator documentation for installation, migration from direct Jellyfin service management, backup/restore, troubleshooting, recovery, and safe downgrade.
- Recruit beta users across local disks, removable storage, SMB, and NFS configurations; close all data-loss, duplicate-process, privilege, and upgrade blockers.

Exit gate:

- No open critical/high security issue, data-corruption issue, duplicate-instance issue, or unbounded restart/I/O hang.
- Upgrade and rollback pass from every published beta on every supported platform.
- Resource-use and recovery-time targets are documented and met by the soak suite.

## Phase 7 — Release candidate and stable release (`v1.0.0-rc.1` → `v1.0.0`)

- Build reproducible release artifacts for macOS arm64, Linux arm64/amd64, and Windows amd64; add Windows arm64 only after its Phase 4 gate passes.
- Sign and notarize macOS artifacts, Authenticode-sign Windows artifacts with the release certificate, publish checksums, SBOMs, provenance attestations, changelog, compatibility table, and upgrade notes.
- Run the complete clean-install, upgrade, rollback, uninstall, storage-fault, process-fault, reboot, and 30-day soak gates against the release commit.
- Publish GitHub Releases and platform packages from an immutable tag through CI; verify downloaded artifacts independently before marking the release stable.
- Define the maintenance policy: supported Jellyfin/OS versions, security-fix process, deprecation window, patch-release cadence, and `v1` API/config compatibility promise.

Stable-release acceptance:

- All three supported OS families pass native lifecycle and destructive fault tests.
- Jellyfin keeps its expected environment and verified hardware-transcoding capability under Remora.
- Storage loss always fences safely, manual stop always wins, recovery never launches a duplicate instance, and upgrades preserve config/state/secrets.

## Deferred beyond v1

- Distributed HA, leader election, fencing other hosts, or Patroni-style cluster consensus.
- SAN-specific health management, container/Kubernetes operators, automatic media repair, and automatic Jellyfin upgrades.
- A remotely exposed management service; v1 remains local-control first.
