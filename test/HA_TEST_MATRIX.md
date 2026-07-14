# High-availability test matrix

Baselines: `test/test.yaml`, Jellyfin 10.10.7 tarball, 10.11.11 app bundle,
and 12.0.0 app bundle on macOS arm64. Destructive runs use only
`/Users/zhoudingpeng/Appdata/jellyfin` for Jellyfin data and restore every
temporary permission, credential, and mount change.

## Automated coverage

The repository currently contains 111 top-level tests. HA-specific coverage includes:

- Supervisor start, healthy transition, graceful stop, fatal storage fencing, configured recovery streak, manual-stop precedence, health-failure threshold restart, single-sample health accounting, transient startup-wizard rejection, bounded first-start initialization retries, five-failure setup/process circuit breakers, administrative circuit reset, serialized concurrent commands, unexpected `SIGKILL`, and `D`/`U` process timeout handling.
- Exact-process adoption, original adopted-process uptime, duplicate-process rejection, stale PID-file rejection, process-group descendant cleanup, kernel argv-boundary identity (including paths with spaces), and macOS environment preservation.
- Required mount-source matching for physical, SMB, NFS, Unicode/escaped SMB shares, isolated timed I/O probes, read-only probes, missing paths, secret redaction, and consecutive per-disk failure thresholds that reset after recovery while preserving fail-closed startup.
- macOS mount-target recreation after Disk Arbitration removes `/Volumes/<share>`, including unsafe-path and symlink rejection.
- Darwin `com.apple.provenance` detection, advisory validation output, and structured daemon warning logging.
- Fenced start rejection, force-stop routing, socket-file safety, duplicate Remora instance locking, and CLI convergence across `PROCESS_FAILED`, `STORAGE_FENCED`, and restart PID replacement.
- `remoractl` validates request construction and pins a validated `localhost` resolution to prevent a second DNS/hosts lookup from changing the control destination.
- Control-plane tests cover version and operation-ID headers, structured safety errors, deterministic CLI exit codes, and bounded/ordered state-transition history.
- Phase 3 control-plane coverage includes concurrent operation IDs, old-client lifecycle compatibility, canceled mutations, actual slow-header disconnection, malformed/oversized request rejection, bounded non-symlink log reads, owner-only socket restart, redacted API-key CRUD, session stop, atomic configuration editing, diagnostic bundle permissions, and managed-tree-only ffmpeg accounting.
- A transient Darwin `U` process state with a healthy API remains `RUNNING`, while continuously uninterruptible `D`/`U` timeout paths still force-kill or open `PROCESS_FAILED` when termination fails.
- Versioned configuration migration preserves legacy heartbeat timing; `remoractl init` rejects invalid edits without replacing an existing configuration, uses owner-only file mode, rejects symlink destinations, and emits a path-correct Darwin launchd plist.
- Jellyfin health success/failure, first-run sequence, bootstrap-user rename, API-key creation/validation, revoked-key rejection, watchdog creation/login/logout, and wrong-password failure propagation.
- Setup selection values are resolved from the same display-language, metadata-language, and country catalogs used by Jellyfin Web; API codes entered as configuration labels fail closed, while omitted selections preserve server defaults.
- Jellyfin 10.11/12 API contract fixtures; setup-wizard XML suppression; configured/unconfigured ownership precedence; atomic backup, idempotence, asset prevalidation, multi-file rollback, and fail-closed process start.
- A new PID cannot inherit the prior PID's healthy result or clear crash history before receiving its own health check.
- Unicode table rendering rejects terminal control characters and aligns CJK paths; structured JSON preserves UID/server/session fields, and Jellyfin 10.11/12 session fixtures cover playing, paused, idle, anonymous, and inactive clients.

Run with:

```sh
go test -race ./...
go vet ./...
```

## Real Jellyfin 12 fault run

All items below passed on 2026-07-13:

| Fault or transition | Expected invariant | Result |
|---|---|---|
| Empty data/config/cache/log directories | Automatic setup, one API key, controlled restart, `RUNNING` | Pass |
| Revoke active Remora API key | Detect 401, authenticate administrator, replace key atomically, no Jellyfin outage | Pass |
| Change watchdog password externally | Sticky `DEGRADED`; ordinary health cannot clear it | Pass |
| Restore watchdog password | Next real login/logout clears degradation | Pass |
| Kill healthy Jellyfin with `SIGKILL` | Backoff and replacement PID | Pass |
| Kill five startup processes before health | `PROCESS_FAILED` circuit opens | Pass |
| Administrative start after circuit opens | Circuit resets and Jellyfin returns to `RUNNING` | Pass |
| Kill Remora with `SIGKILL` | Jellyfin survives; replacement Remora adopts the same PID | Pass |
| Start a second Remora | Instance lock rejects it before supervision; original PID remains | Pass |
| Remove cache write permission | Jellyfin stops and state becomes `STORAGE_FENCED` | Pass |
| Start while fenced | Control API returns 409 | Pass |
| Stop while fenced, then restore permission | Manual stop wins; Jellyfin remains stopped | Pass |
| Administrative start after storage recovery | Recovery streak completes and one new PID starts | Pass |
| Ordinary restart | PID changes and no false `FIRST_START` transition occurs | Pass |
| Enable Phase 2 performance/branding values in `test/test.yaml` | Reconcile before start, preserve Jellyfin-owned XML, create backup, reach healthy, and retain values after restart/stop | Pass |
| Normal Disk Arbitration unmount of live Unicode SMB share | Enter `STORAGE_FENCED`, stop the old PID, never restart against a missing mount | Pass |
| Restore the same live SMB share | Healthy recovery completes and exactly one replacement PID starts | Pass |
| Final stop | PID is zero; no Jellyfin/Remora process or control socket remains | Pass |

The live SMB run used `diskutil unmount` without force and affected only `/Volumes/nas_STORAGE_公共空间`; the other two SMB mounts remained mounted. The old Jellyfin PID exited before recovery and one replacement PID reached `RUNNING`. This run also exposed that Disk Arbitration deletes the mount-point directory. Remora now recreates a missing mount target before SMB/NFS/APFS mount attempts; the LaunchDaemon must run with the documented root privilege to recreate targets under `/Volumes`.

The configured APFS source and live Unicode SMB mount were otherwise validated healthy before every destructive run. Hardware discovery continued to report VideoToolbox, OpenCL, arm64, and all ten CPU cores.

An opt-in clean-install integration test was also run against Jellyfin 10.11.11
on 2026-07-14. Three isolated servers were initialized with the exact Web UI
labels `العربية`/`Arabic`/`Saudi Arabia`, `한국어`/`Korean`/`Korea`, and
`Deutsch`/`German`/`Germany`. All three completed setup and persisted the
expected `ar`/`SA`, `ko`/`KR`, and `de`/`DE` internal values. Run it with:

```sh
JELLYFIN_INTEGRATION_BIN=/Applications/Jellyfin.app/Contents/MacOS/jellyfin \
JELLYFIN_INTEGRATION_WEB=/Applications/Jellyfin.app/Contents/Resources/jellyfin-web \
go test -run '^TestInstalledJellyfinWebSelectionLabels$' -v ./internal/jellyfin
```

## Real Jellyfin 10.11.11 compatibility and fault run

The full matrix above was repeated on 2026-07-13 after a clean 10.11.11
installation. Clean setup, bootstrap-user rename, API-key/watchdog provisioning,
pre-start XML reconciliation, key revocation replacement, sticky watchdog
degradation/recovery, healthy-process crash replacement, Remora crash adoption,
duplicate-instance rejection, write-permission fencing/recovery, manual-stop
precedence, ordinary restart, and live SMB unmount/recovery passed. The same
process environment exposed VideoToolbox, OpenCL, arm64, ten CPU cores, and
Jellyfin's bundled FFmpeg without Remora-specific overrides.

The first real startup-crash circuit run exposed stale health inheritance: a
replacement PID could briefly reuse the preceding PID's healthy sample and clear
the crash window. Remora now clears process-scoped health on every successful
spawn. The corrected five-startup-crash run entered `PROCESS_FAILED`, and an
administrative start reset the circuit and returned exactly one process to
`RUNNING`.

During the foreground non-root test, Disk Arbitration removed the SMB mount
directory and Remora correctly remained fenced because it could not recreate a
directory under `/Volumes`. The share was restored through macOS's user mount
service, after which the configured recovery streak started exactly one new
Jellyfin PID. The documented root LaunchDaemon can recreate that target directly.

## Real Jellyfin 10.10.7 tarball compatibility and fault run

The arm64 archive (local SHA-256
`f3cfdb7ac9600dd85936274250ca3e0ffa594b2cc9938719812270e7222b5958`) was
extracted to a sibling `jellyfin`/`jellyfin-web` layout on 2026-07-14. After the
unsigned main Mach-O was locally ad-hoc signed, Remora completed a clean setup
and reached `RUNNING`.
All configured system, branding, encoding, and networking XML values were
reconciled; administrator, watchdog user, and API key provisioning passed.

Ordinary restart replaced the PID. A forced Remora exit left Jellyfin alive and
the replacement Remora adopted the same PID. Write-permission loss fenced and
stopped Jellyfin, and the three-check recovery streak launched one replacement.
API-key revocation returned 204 and Remora atomically replaced the key without a
Jellyfin outage. Live SMB disappearance fenced the service; restoring the user
mount returned one process to `RUNNING`. Foreground non-root automatic mount
could not recreate the deleted `/Volumes` target, matching the documented
LaunchDaemon privilege requirement.

## Deliberately non-destructive substitutions

- A forced SMB unmount and abrupt NAS power/network loss were not used. The normal live unmount covers mount disappearance, fencing, old-process termination, and recovery; unreachable-server timeout behavior remains covered deterministically.
- The Mac was not forced to sleep or reboot. Remora crash/restart, stale socket replacement, process adoption, and storage disappearance/recovery cover the component invariants; actual sleep/wake and reboot remain Phase 1 system tests.
- A real uninterruptible kernel `D`/`U` process was not manufactured. Both timeout branches are covered through an injected process backend, including force-kill success and failure.
