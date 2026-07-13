# Jellyfin Remora development roadmap

This roadmap takes the project from the current macOS prototype to a supported, reproducible release on macOS, Linux, and Windows. Versions are milestone labels rather than promised dates. A phase is complete only when its exit gate passes.

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

- Config schema v1, strict unknown-key rejection, legacy heartbeat aliases, and checked-in JSON Schema.
- Non-mutating `validate-config`, protected `--prepare`, credential-file permission checks, and isolated timed I/O probes.
- APFS plus SMB validation with Unicode share names, URL-encoded mount sources, and Bonjour service-name/IP equivalence.
- Apple Silicon smoke test against Jellyfin 12.0.0: first-run database creation, hardware capability discovery, `/health` transition to `RUNNING`, and graceful stop all passed.
- Clean-directory setup against Jellyfin 12.0.0: OS-account bootstrap-user handling, configured administrator rename, setup completion, owner-only API-key persistence, watchdog-user creation, controlled restart, and periodic login/logout all passed.
- Normal-restart regression coverage confirms Jellyfin 12's transient setup-listener state is ignored until core health and a stable incomplete-wizard streak agree; existing installations no longer flash into `FIRST_START`.
- Destructive local fault test using `test/test.yaml`: write-permission loss fenced and stopped Jellyfin; restoring permission required the configured recovery streak and launched exactly one replacement process. Explicit restart and manual stop also passed.
- The repository now covers 64 top-level tests plus real Jellyfin fault injection: crash-loop circuit breaking, Remora crash adoption, duplicate-instance locking, API-key revocation recovery, sticky watchdog degradation, process-group cleanup, stale PID rejection, live SMB unmount fencing/recovery, `D`/`U` timeout branches, and fail-closed XML reconciliation. See `test/HA_TEST_MATRIX.md`.
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
- Add configurable per-check failure/recovery thresholds, mount retry limits, jittered restart backoff, and explicit administrative un-fence/reset operations.
- Build `install`, `uninstall`, and extended `diagnose` workflows; `validate-config` and protected directory preparation are already available.
- Test both Apple Silicon and Intel builds against Jellyfin 10.11.x and the current 12.x-compatible API surface.

Exit gate:

- Real APFS/removable-volume, SMB, and NFS fault tests demonstrate: disconnect causes fencing, no false local data tree is created, the old process exits, and recovery requires the configured healthy streak.
- A 7-day soak test covers media playback/transcoding, sleep/wake, network interruption, Remora restarts, log rotation, and repeated Jellyfin crashes.
- Signed development artifacts install, upgrade, and uninstall cleanly through launchd on arm64 and amd64 macOS.

## Phase 2 — Jellyfin lifecycle and configuration management (`v0.3.0-alpha`)

Completed in the `test/test.yaml` iterations:

- Typed Jellyfin API client foundations with public version/setup discovery, structured API errors, Jellyfin authorization headers, and `httptest` contract coverage.
- First-run detection and complete setup sequence: initial configuration, Jellyfin 12 bootstrap user, remote access, completion, configured administrator rename/login, and Remora API-key creation.
- Atomic API-key persistence with owner-only permissions and idempotent reuse of an existing `Jellyfin Remora` key.
- Optional login watchdog that creates a missing configured user and performs periodic real login, `/Users/Me`, and logout checks without logging credentials.
- Versioned public-info, setup-user, authentication-result, and API-key contract fixtures for Jellyfin 10.11.x and 12.x; revoked API-key detection and automatic replacement.
- Typed, atomic, backup-first XML reconciliation for supported general, branding, playback/transcoding, and networking fields before every process start.
- Explicit ownership precedence: configured Remora fields win before startup, while unspecified fields and dashboard-managed data remain byte-for-byte untouched.
- Setup-wizard write suppression, custom CSS/image validation before the first write, XML mode/ownership preservation, idempotence, and multi-file rollback on failure.
- Real Jellyfin 12.0.0 validation using `test/test.yaml`: configured performance and branding values were applied before start, `/health` reached `RUNNING`, one-instance restart passed, and graceful stop preserved the values.

Exit gate:

- Clean-directory tests automatically initialize Jellyfin, create an API key, restart, and become healthy without manual web setup.
- Existing-server tests prove unspecified settings and user-managed data remain unchanged.
- Contract tests pass against supported 10.11.x and 12.x server fixtures.

Phase 2 exit gate passed on macOS arm64 on 2026-07-13. Native Intel macOS
coverage remains part of the Phase 1 platform release gate rather than this
feature milestone.

## Phase 3 — Complete control plane and observability (`v0.4.0-alpha`)

- Stabilize and document `/v1` request/response schemas, error codes, operation IDs, status enums, and compatibility rules.
- Complete `remoractl logs`, `edit-config`, `apikey list/create/delete`, and `session list/stop`.
- Extend status with process start identity, CPU/RSS, listening endpoints, storage latency, active sessions, playing users, and Remora-owned ffmpeg/transcode counts.
- Add bounded event history, structured diagnostic bundles, and Prometheus-compatible metrics on a separate opt-in loopback endpoint.
- Add concurrent-client, slow-client, cancellation, malformed-request, socket-permission, and daemon-restart tests.
- Keep remote control disabled by default. If non-loopback control is later enabled, require TLS and scoped authentication rather than reusing Jellyfin credentials.

Exit gate:

- Every CLI action has a matching documented API operation and deterministic exit codes.
- API compatibility tests demonstrate that an older supported `remoractl` can control a newer daemon within the same major version.

## Phase 4 — Linux support (`v0.6.0-alpha`)

- Implement the Linux platform backend using `/proc`, pidfd where available, process groups, child-subreeper behavior, mountinfo/statfs, and cgroup awareness without imposing CPU/GPU limits.
- Support physical filesystems, `mount.cifs`, and NFS; add libsecret/file-based credential-provider interfaces and protocol-specific timeout guidance.
- Provide systemd units with correct foreground behavior, runtime/state directories, privilege separation, restart limits, shutdown ordering, and network/mount dependencies.
- Support existing-process adoption, zombie/uninterruptible-state detection, descendant cleanup, and ffmpeg accounting on cgroup v2 and non-cgroup systems.
- Package portable tarballs first, then `.deb` and `.rpm`; provide install, upgrade, rollback, and purge tests in clean VMs/containers.

Exit gate:

- Integration matrices pass on representative current Debian/Ubuntu, Fedora/RHEL-family, and one rolling distribution, on amd64 and arm64.
- SMB/NFS disconnect, read-only remount, stale mount, full disk, permission loss, process hang, systemd restart, and host reboot tests pass.

## Phase 5 — Windows support (`v0.8.0-alpha`)

- Implement a native Windows Service as the supported deployment and retain Task Scheduler compatibility for non-service installations.
- Use `CreateProcess` semantics with an explicit inherited environment, restricted service identity, Job Objects for process-tree ownership, graceful console/service control, and forced termination fallback.
- Implement volume discovery by drive/volume GUID, filesystem and write probes, UNC/SMB reachability, reconnect handling, and Windows Credential Manager integration.
- Replace Unix sockets with a secured named pipe while retaining loopback REST; use Windows ACLs for configuration, state, socket/pipe, logs, and secrets.
- Collect process CPU/RSS, listening ports, child ffmpeg processes, and service events through supported Windows APIs; integrate with Windows Event Log.
- Produce signed `.zip` and MSI artifacts with install, repair, upgrade, rollback, and uninstall coverage.

Exit gate:

- Windows 10/11 and supported Windows Server VM matrices pass on amd64; arm64 is released only if the complete native dependency and Jellyfin test matrix passes.
- Reboot, service-account change, SMB credential expiry, share disconnect, drive-letter change, hung process, forced stop, and upgrade tests pass.

## Phase 6 — Cross-platform beta hardening (`v0.9.x-beta`)

- Freeze configuration schema v1, state-file compatibility, REST API v1, CLI exit codes, service names, filesystem locations, and upgrade rules.
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

- Build reproducible release artifacts for macOS arm64/amd64, Linux arm64/amd64, and Windows amd64; add Windows arm64 only after its Phase 5 gate passes.
- Sign and notarize macOS artifacts, Authenticode-sign Windows artifacts, publish checksums, SBOMs, provenance attestations, changelog, compatibility table, and upgrade notes.
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
