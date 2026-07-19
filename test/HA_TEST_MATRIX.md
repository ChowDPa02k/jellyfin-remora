# High-availability test matrix

Baselines: `test/test.yaml`, Jellyfin 10.10.7 tarball, 10.11.11 app bundle,
and 12.0.0 app bundle on macOS arm64. Destructive runs use only
`/Users/zhoudingpeng/Appdata/jellyfin` for Jellyfin data and restore every
temporary permission, credential, and mount change.

## Automated coverage

The automated suite's HA-specific coverage includes:

- Supervisor start, healthy transition, graceful stop, fatal storage fencing, configured recovery streak, manual-stop precedence, health-failure threshold restart, single-sample health accounting, transient startup-wizard rejection, bounded first-start initialization retries, five-failure setup/process circuit breakers, administrative circuit reset, serialized concurrent commands, unexpected `SIGKILL`, `D`/`U` process timeout handling, and durable `DATABASE_DAMAGED` fencing after console-plus-API confirmation.
- Exact-process adoption, original adopted-process uptime, duplicate-process rejection, stale PID-file rejection, process-group descendant cleanup, kernel argv-boundary identity (including paths with spaces), and macOS environment preservation.
- Managed-process environment tests verify complete inheritance, configured overrides, explicit empty values, and Remora's console defaults through a real child process on Darwin/Linux; config and embedded-sample tests cover all three platform templates.
- Property coverage drives 100 independently seeded, 128-step command sequences through the real reconcile function and continuously checks manual-stop/fence dominance, at-most-one live generation, bounded and ordered events, and lifecycle accounting.
- Native Go fuzz targets cover strict YAML migration/validation, REST JSON bodies and query parameters, forward-compatible state-file fields, archive entry containment, and malformed ZIP/TAR.GZ/TAR.XZ streams. CI executes their seed corpora under `go test`; maintainers can run sustained mutation with the commands below.
- Lifecycle state and PID-file writes, process start/stop, atomic Jellyfin XML replacement, blocked storage probes, and mount-helper termination expose injected failure paths. Start/stop/restart mutations now roll back when their durable acknowledgement fails, including a second-location failure after the first state write succeeds.
- Restart-interruption tests cover accepted stop before reconciliation, the gap between old-process exit and replacement start, restart-attempt circuit opening after exactly five failures, and Linux replacement adoption without a duplicate process.
- Required mount-source matching for physical, SMB, NFS, Unicode/escaped SMB shares, isolated timed I/O probes, read-only probes, missing paths, secret redaction, Darwin's media-oriented `vers=3,resvport,nolocks,rsize=65536,wsize=65536,intr,soft` default with explicit-policy/NFSv4 preservation, and consecutive per-disk failure thresholds that reset after recovery while preserving fail-closed startup.
- macOS mount-target recreation after Disk Arbitration removes `/Volumes/<share>`, including unsafe-path and symlink rejection.
- Darwin `com.apple.provenance` detection, advisory validation output, and structured daemon warning logging.
- Fenced start rejection, force-stop routing, socket-file safety, duplicate Remora instance locking, and CLI convergence across `PROCESS_FAILED`, `STORAGE_FENCED`, and restart PID replacement.
- `remoractl` validates request construction and pins a validated `localhost` resolution to prevent a second DNS/hosts lookup from changing the control destination.
- Unix control-plane coverage starts a real HTTP server on a port-qualified socket in an isolated runtime directory, verifies automatic discovery and transport use, and keeps explicit socket selection available for ambiguous directories.
- Control-plane tests cover version and operation-ID headers, structured safety errors, deterministic CLI exit codes, and bounded/ordered state-transition history.
- Log isolation coverage keeps Jellyfin stdout/stderr out of Remora's structured records, verifies the Darwin child sees a raw TTY without losing process-group isolation, preserves ANSI bytes and LF endings, tails the dedicated managed console file by source, follows appends and active-file rotation, and verifies lumberjack backup naming.
- Phase 3 control-plane coverage includes concurrent operation IDs, old-client lifecycle compatibility, canceled mutations, actual slow-header disconnection, malformed/oversized request rejection, bounded non-symlink log reads, owner-only socket restart, redacted API-key CRUD, session stop, atomic configuration editing, diagnostic bundle permissions, and managed-tree-only ffmpeg accounting.
- A transient Darwin `U` process state with a healthy API remains `RUNNING`, while continuously uninterruptible `D`/`U` timeout paths still force-kill or open `PROCESS_FAILED` when termination fails.
- After application readiness is established, a transient `/System/Info/Public` failure cannot override a healthy `/health` result or trigger a restart; before readiness, the same split setup-listener state remains `STARTING`.
- Graceful API shutdown is race-safe: if Jellyfin exits before the following process-group signal, the missing process is accepted as a successful stop instead of opening `PROCESS_FAILED`.
- Exiting Remora after an earlier manual stop preserves `STOPPED` without a duplicate `STOPPING` transition or an unnecessary Jellyfin shutdown API request.
- Darwin rejects Windows named-pipe, volume-GUID/label/filesystem, and Credential Manager fields instead of silently accepting unenforced identity constraints.
- Versioned configuration migration preserves legacy heartbeat timing; `remoractl init` rejects invalid edits without replacing an existing configuration, uses owner-only file mode, rejects symlink destinations, and emits a path-correct Darwin launchd plist.
- Jellyfin health success/failure, first-run sequence, bootstrap-user rename, API-key creation/validation, revoked-key rejection, persistent watchdog-session reuse, rejected-token reauthentication, and wrong-password failure propagation.
- Setup selection values are resolved from the same display-language, metadata-language, and country catalogs used by Jellyfin Web; API codes entered as configuration labels fail closed, while omitted selections preserve server defaults.
- Jellyfin 10.11/12 API contract fixtures; setup-wizard XML suppression; configured/unconfigured ownership precedence; atomic backup, idempotence, asset prevalidation, multi-file rollback, and fail-closed process start.
- A new PID cannot inherit the prior PID's healthy result or clear crash history before receiving its own health check.
- Unicode table rendering rejects terminal control characters and aligns CJK paths; structured JSON preserves UID/server/session fields, and Jellyfin 10.11/12 session fixtures cover playing, paused, idle, anonymous, and inactive clients.
- Linux mountinfo/statfs parsing and capacity rejection, exact `/proc` identity,
  listening-port discovery, pidfd signaling, process-group descendant cleanup,
  child-subreaper adoption/reaping, cgroup-v2 escaped-ffmpeg accounting, systemd
  generation/install idempotence and missing-config conditions, run-as-user
  storage probes, and fence-before-
  remount recovery behavior. Timeout tests prove that an unkillable path probe
  or mount helper returns control without stacking another helper for the same
  target. Pre-sample crash cleanup attributes same-process-group descendants on
  non-cgroup systems and excludes Remora's own concurrent storage probe from the
  systemd cgroup orphan fallback. Network-storage host parsing covers DNS,
  IPv4, bracketed IPv6, SMB credentials, conventional NFS syntax, and the
  legacy `server/export` form.

Run with:

```sh
go test -race ./...
go vet ./...
```

Run sustained parser mutation one target at a time, for example:

```sh
go test ./internal/config -fuzz=FuzzParseConfiguration -fuzztime=10m
go test ./internal/config -fuzz=FuzzMigrateConfiguration -fuzztime=10m
go test ./internal/control -fuzz=FuzzAPIJSONBody -fuzztime=10m
go test ./internal/control -fuzz=FuzzAPIQueryParsing -fuzztime=10m
go test ./internal/supervisor -fuzz=FuzzPersistedStateParsing -fuzztime=10m
go test ./internal/kickstart -fuzz=FuzzArchiveEntryName -fuzztime=10m
go test ./internal/kickstart -fuzz=FuzzInspectArchiveBytes -fuzztime=10m
```

Kickstart package-validator unit coverage uses bounded local HTTP servers to
assert official mirrorlist SHA-256 matching, legacy archive download/hash
fallback, checksum rejection, connection/request deadlines, whole-download
deadlines, visible TUI progress phases, and rejection when a verified archive
changes before extraction. These tests do not depend on the public repository.

Portable Linux tarballs have a fixed-epoch reproducibility and manifest test:

```sh
./test/package_linux_tar.sh
# On a host with dpkg-deb or rpmbuild/rpm:
./test/package_linux_native.sh amd64 deb
./test/package_linux_native.sh amd64 rpm
```

All three Linux artifact formats emit sibling SHA-256 files. Native DEB and RPM
builders verify the checksum against the finished package in their respective
Debian and Rocky build environments.

The native `/proc`/pidfd/subreaper/statfs backend can also be executed in the
Debian, Ubuntu, Fedora, and openSUSE Tumbleweed container baselines on either
Linux architecture. This is an ABI/distribution check, not a substitute for
the real systemd/Jellyfin/fault matrix:

```sh
LINUX_TEST_ARCH=arm64 ./test/linux_container_matrix.sh
```

CI runs the amd64 and arm64 containers on matching native GitHub-hosted Linux
runners; process-group, pidfd, fork/exec, and child-subreaper behavior is not
claimed from user-mode QEMU. Each container also has a bounded five-minute
default (`LINUX_CONTAINER_TIMEOUT`), and CI caps the complete distribution job
at 30 minutes, so a broken container runtime fails with the affected image
instead of occupying a runner indefinitely.

On a disposable native Linux host with an installed package and a healthy
configuration, the destructive systemd and physical-identity smoke tests are
repeatable with:

```sh
sudo ./test/linux_real_systemd.sh
sudo ./test/linux_real_storage_fence.sh /path/to/configured/physical/mount
sudo ./test/linux_real_network_fence.sh /path/to/network/mount SERVER-IP TCP-PORT
sudo ./test/linux_real_permission_fence.sh /path/to/jellyfin/config
sudo ./test/linux_real_filesystem_faults.sh /path/to/disposable/physical/mount
sudo ./test/linux_real_process_hang.sh
sudo REMORA_TEST_DATABASE=/path/on/nfs/jellyfin.db \
  REMORA_DATABASE_DAMAGE_CONFIRM=YES-DESTROY-DISPOSABLE-DATABASE \
  ./test/linux_real_database_damage.sh
```

The first test proves Remora-only crash adoption and normal full-tree service
restart semantics. The second overlays the physical target with the wrong
filesystem, requires `STORAGE_FENCED` with no Jellyfin process, then removes
the overlay and requires controlled single-instance recovery. The third uses a
temporary nftables output rule, detaches an NFS or SMB mount, and verifies both
bounded failure handling and Remora-controlled remount after network recovery.
The fourth removes write permission from a managed Jellyfin path and proves
that the health probe really runs as the configured Jellyfin identity even when
the Remora service itself runs as root. The final test requires a disposable,
dedicated filesystem: it verifies both a read-only remount and exhaustion of
all user-available blocks, restoring the filesystem after each fence. The
read-only case uses a self-bind view so it remains reproducible even when the
kernel refuses to remount a live database filesystem read-only with `EBUSY`.
The process-hang test sends `SIGSTOP`, requiring the API watchdog and bounded
stop escalation to kill the unresponsive tree before starting one replacement.
The database-damage harness refuses non-NFS paths and requires an explicit
destructive confirmation. It snapshots the database only while Jellyfin is
stopped and, when a fresh database could fit entirely in SQLite's page cache,
adds valid disposable activity rows after the snapshot. It then destroys the
live disposable copy, requires durable
`DATABASE_DAMAGED` fencing across a Remora service restart, restores the
snapshot, and verifies explicit-start recovery. Restoration removes every
pressure row.

## Real database-damage fault run

A disposable Jellyfin 12.0.0 instance on macOS arm64 was initialized beneath
`/Users/zhoudingpeng/Appdata/jellyfin/db-damage-uat` on 2026-07-16. After a
clean stopped backup, an external test writer destroyed every page of the live
`jellyfin.db` while PID 88103 remained running. Jellyfin's `/health` continued
to return 200, while authenticated Users, Items, and ActivityLog reads all
returned 500 and the managed console emitted `SQLite Error 11: database disk
image is malformed`.

Remora stopped the old PID, entered `DATABASE_DAMAGED`, wrote fourth-line state
value 1, and retained the fence after the Remora daemon was restarted. A
`remoractl restart` attempt returned conflict exit 4. Restoring the stopped
backup and removing its obsolete WAL/SHM sidecars followed by explicit
`remoractl start` produced one healthy replacement PID 92815 and fourth-line
state value 0. All disposable data and configuration were then removed. This
run also proves `/health` alone is insufficient for this failure class; the
periodic read-only database-backed probes are required.

The NFS/no-lock repeat passed on 2026-07-16 with Jellyfin 10.11.11 running on
Debian 13 and its 70.8 MB disposable database stored in Rocky Linux 10.1's
`192.168.1.109:/data` export over NFSv3 `nolock,soft`. Remora stopped managed
PID 1164263, rejected `restart`, retained `DATABASE_DAMAGED` across a dedicated
systemd service restart, then restored the stopped snapshot and reached
`RUNNING` with replacement PID 1164913 only after explicit `start`. The
existing Debian UAT Remora/Jellyfin instance remained online on separate ports
throughout the run.

## Real native Linux amd64 compatibility and fault runs

Jellyfin 10.11.11 was exercised on Rocky Linux 10.1 (RPM-family) and Debian
13.6 on 2026-07-16. Both hosts used native systemd services, cgroup v2, dedicated
ext4 loop-backed application storage, and the distribution Jellyfin service was
disabled so Remora remained the sole supervisor.

| Host / fault or transition | Expected invariant | Result |
|---|---|---|
| Rocky clean initialization | Direct ELF process runs as `jellyfin`, reaches `RUNNING`, one PID | Pass |
| Debian clean initialization | Direct ELF process runs as `jellyfin`, reaches `RUNNING`, one PID | Pass |
| Unexpected Remora `SIGKILL` on both hosts | systemd restarts Remora and adopts the unchanged Jellyfin PID | Pass |
| Normal systemd stop/start on both hosts | Complete old tree is removed; exactly one replacement starts | Pass |
| Physical UUID disappears/unmounts on both hosts | Fence and stop before any remount; recover only after identity returns | Pass |
| Rocky read-only application filesystem | Remain fenced with no Jellyfin PID until read/write access returns | Pass |
| Rocky full application filesystem | Real write/capacity probe fences; freeing space permits controlled recovery | Pass |
| Debian NFS server loss and recovery | Bounded probe fences; restored export yields one replacement PID | Pass |
| Debian SMB server loss and recovery | Bounded probe fences; restored share yields one replacement PID | Pass |
| Debian config permission loss | Probe runs as the Jellyfin identity, fences despite root Remora | Pass |
| Debian stopped/hung Jellyfin | Timeout forces the old tree down and starts one healthy replacement | Pass |
| Rocky ffmpeg moved outside ancestry but kept in the service cgroup | Status counts it and forced restart removes it | Pass |
| Rocky Jellyfin root `SIGKILL` with escaped ffmpeg in the service cgroup | Exit callback validates cached start identity, kills the orphan, and starts exactly one replacement | Pass |
| Rocky root `SIGKILL` immediately after moving an unsampled ffmpeg into the service cgroup | Self-cgroup fallback finds and kills it before one replacement reaches `RUNNING` | Pass |
| Debian DEB install/upgrade/rollback/purge/reinstall | Versions change correctly; purge preserves operator config and Jellyfin data | Pass |
| Rocky RPM install/upgrade/rollback/erase/reinstall | Versions change correctly; erase preserves operator config and Jellyfin data | Pass |
| Debian host reboot | New boot ID; systemd starts Remora, loop-backed physical storage plus NFS/SMB recover, and exactly one Jellyfin 10.11.11 reaches `RUNNING` | Pass |
| Rocky host reboot | New boot ID; loop-backed physical storage, one Remora, one Jellyfin, NFS, and SMB recover | Pass |
| Rocky `jellyfin.env` propagation | An isolated child retains an unlisted parent value, receives YAML overrides and an explicit empty value, then exits cleanly | Pass (2026-07-18) |
| Debian `jellyfin.env` propagation | Same full process-boundary assertions using the Debian run-as-user backend | Pass (2026-07-18) |
| Rocky interrupted-supervisor adoption | Replacement manager adopts the exact live PID and refuses a duplicate start | Pass (2026-07-18) |
| Debian interrupted-supervisor adoption | Replacement manager adopts the exact live PID and refuses a duplicate start | Pass (2026-07-18) |
| Rocky/Debian forced descendant cleanup | A managed child process is gone after process-group force stop on both kernels | Pass (2026-07-18) |

The destructive amd64 matrix was repeated against the packaged
`0.8.0-alpha.7` binaries from core commit `5e9536ad1b20` on 2026-07-16. The
repeat used the checked-in `linux_real_*.sh` harnesses and covered RPM/DEB
upgrade, Remora-only `SIGKILL` adoption, normal systemd restart, wrong-device
overlay, read-only bind view, zero user-available blocks, run-as-user permission
loss, NFS and SMB port loss plus Remora-controlled remount, and a `SIGSTOP`
Jellyfin hang. Debian then rebooted from boot ID
`ba575bff-add0-47d8-a6e9-699f48bc83e9` to
`69a9e038-0dc9-495c-84ec-8b098fcac2c2` with all seven probes healthy. Rebooting
the Rocky storage server changed its boot ID from
`9339afaa-01bf-41b8-94f2-94335ec22e3e` to
`3d581448-4ae6-4568-a847-a03a416a05d1`; during that outage Debian fenced both
network mounts and cleared its Jellyfin PID in nine seconds, then remounted NFS
and SMB and returned to one `RUNNING` Jellyfin after Rocky recovered.

The checked-in container matrix extends the native-systemd/Jellyfin baseline to
Ubuntu 24.04 LTS and openSUSE Tumbleweed. It builds both images from their
current distribution repositories, installs the native `0.8.0-alpha.8` DEB or
RPM from commit `001a58c8f40f`, extracts the official Jellyfin 10.11.11 server
DEB, and gives each container a dedicated 4 GiB ext4 loop filesystem. Run it on
a disposable rootful Podman host with:

```sh
sudo ./test/linux-systemd/run-container-matrix.sh \
  /path/to/jellyfin-remora_0.8.0~alpha.8_amd64.deb \
  /path/to/jellyfin-remora-0.8.0-0.alpha.8.el10.x86_64.rpm \
  /path/to/jellyfin-server_10.11.11+deb13_amd64.deb \
  /usr/share/jellyfin-web
```

| Native systemd container case | Ubuntu 24.04 | openSUSE Tumbleweed |
|---|---:|---:|
| Real Jellyfin 10.11.11 first-start provisioning and `/health` | Pass | Pass |
| Remora `SIGKILL` restart with unchanged Jellyfin PID adoption | Pass | Pass |
| Normal systemd stop/start with complete old-tree removal | Pass | Pass |
| Wrong physical-filesystem identity fence and recovery | Pass | Pass |
| Jellyfin-user permission loss fence and recovery | Pass | Pass |
| Read-only filesystem fence and recovery | Pass | Pass |
| Zero user-available blocks fence and recovery | Pass | Pass |
| `SIGSTOP` API failure, forced stop, and single replacement | Pass | Pass |

This container matrix is additive to the physical Debian/Rocky runs: it closes
the Ubuntu and rolling-distribution compatibility gate, while network-storage
disconnects and host reboots remain proven by the physical hosts above.

The complete Go suite passed in a Debian arm64 container. The arm64 native
backend and capacity suite also passed in Debian 13, Ubuntu 24.04, Fedora, and
openSUSE Tumbleweed containers on a matching native GitHub-hosted ARM kernel.
This establishes the Linux syscall ABI and distribution-userland baseline.

## Real native Linux arm64 compatibility and fault run

The official Jellyfin 10.11.11 Debian 13 arm64 server package and packaged
Remora `0.8.0-alpha.8` DEB ran on a native Ubuntu 24.04 ARM GitHub-hosted runner
on 2026-07-16. The checked-in `test/linux_real_arm64_jellyfin.sh` gate verifies
the package checksums and aarch64 ELF before creating a dedicated 4 GiB ext4
filesystem and native systemd instance. The complete 17-job workflow passed in
[CI run 29482619964](https://github.com/ChowDPa02k/jellyfin-remora/actions/runs/29482619964).

| Native arm64 case | Expected invariant | Result |
|---|---|---|
| First start and `/health` | Jellyfin 10.11.11 reaches `RUNNING` as the unprivileged `jellyfin` user | Pass |
| Unexpected Remora `SIGKILL` | systemd restarts Remora and adopts the unchanged Jellyfin PID | Pass |
| Normal systemd stop/start | Complete old process tree is removed; exactly one replacement starts | Pass |
| Wrong physical-filesystem identity | Jellyfin stops while the foreign overlay remains mounted; correct identity recovers once | Pass |
| Config permission loss | The run-as-user probe fences despite privileged Remora; restored permission recovers once | Pass |
| Read-only application filesystem | Jellyfin remains fenced until the read/write view returns | Pass |
| Zero user-available blocks | A failed managed-path write fences; freeing space permits controlled recovery | Pass |
| Jellyfin `SIGSTOP` | API timeout and bounded escalation remove the hung tree and start one replacement | Pass |

The final status reported native `aarch64`, Jellyfin `10.11.11`, `RUNNING`, and
all storage probes healthy. Network-storage disconnects and client/server host
reboots were not repeated on arm64; those architecture-independent Phase 5
transitions remain covered by the physical Debian/Rocky amd64 hosts above.

An AlmaLinux 9 arm64 privileged container additionally ran native systemd and
installed the cross-built aarch64 RPM. This matrix uses the repository fake
Jellyfin server, so it validates package/systemd/process semantics rather than
claiming real arm64 Jellyfin compatibility:

| arm64 systemd transition | Expected invariant | Result |
|---|---|---|
| Install RPM and enable static unit | Root Remora drops the child to `jellyfin`; fake server reaches `RUNNING` | Pass |
| Kill Remora main process | systemd starts a new Remora and preserves/adopts the exact fake-server PID | Pass |
| Normal systemd stop/start | Old fake-server process is removed and exactly one replacement starts | Pass |
| Exceed `StartLimitBurst=5` | Unit enters `failed`, child remains available; reset/start adopts the same PID | Pass |

This earlier fake-server matrix remains useful for RPM/systemd start-limit
coverage; real arm64 Jellyfin and destructive local-storage coverage is provided
by the native Ubuntu gate above.

The Debian reboot run was initially mistaken for a failed VM power-on because
DHCP changed the guest address. After locating the guest at `192.168.1.102`, the
new boot was verified with boot ID `ba575bff-add0-47d8-a6e9-699f48bc83e9`:
`jellyfin-remora.service` was enabled and active, the distribution
`jellyfin.service` remained inactive, the loop-backed ext4 filesystem and the
configured NFS and CIFS mounts were present, all seven storage probes were
healthy, and exactly one Remora plus one Jellyfin 10.11.11 process reached
`RUNNING` as uid 101 (`jellyfin`).

## Real Jellyfin 12 fault run

All items below passed on 2026-07-13:

| Fault or transition | Expected invariant | Result |
|---|---|---|
| Empty data/config/cache/log directories | Automatic setup, one API key, controlled restart, `RUNNING` | Pass |
| Revoke active Remora API key | Detect 401, authenticate administrator, replace key atomically, no Jellyfin outage | Pass |
| Change watchdog password externally | Sticky `DEGRADED`; ordinary health cannot clear it | Pass |
| Restore watchdog password | Next session validation and reauthentication clears degradation | Pass |
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

## Real Darwin NFSv4 fault run

A live NFS export at `192.168.1.109:/data` was tested on 2026-07-15 with
Jellyfin 12.0.0. macOS mounted it with `vers=4,resvport`; the configured mount
target identified the NFS filesystem while `probe-path` selected an exported
writable subdirectory. Remora's create, write, fsync, close, and delete probe
passed, and all configured storage entered the test healthy.

The baseline reached `RUNNING` with Jellyfin PID `82392`. A normal
`diskutil unmount` transitioned through `STOPPING` to `STORAGE_FENCED`, removed
the old PID, and reported the export reachable but not mounted. The foreground
non-root Remora could not remount it and did not treat the recreated local mount
directory as healthy. After an administrator remounted the same NFSv4 export,
the configured three-check recovery streak completed and exactly one
replacement Jellyfin process, PID `86208`, returned to `RUNNING`. The test ended
with a controlled stop, no Jellyfin or Remora process, and the NFS export
unmounted.

An initial NFSv3 mount exposed a server configuration failure rather than a
Remora failure: macOS rejected remote locks because the Rocky Linux server was
not running `rpc.statd`. The README now recommends NFSv4 and documents how to
enable and verify `rpc.statd`/`nlockmgr` when NFSv3 is required; local or
disabled locking remains explicitly unsuitable for production Jellyfin data.

## Deliberately non-destructive substitutions

- A forced SMB unmount and abrupt NAS power/network loss were not used. The normal live unmount covers mount disappearance, fencing, old-process termination, and recovery; unreachable-server timeout behavior remains covered deterministically.
- The Mac was not forced to sleep or reboot. Remora crash/restart, stale socket replacement, process adoption, and storage disappearance/recovery cover the component invariants; actual sleep/wake and reboot remain Phase 1 system tests.
- A real uninterruptible kernel `D`/`U` process was not manufactured. Both timeout branches are covered through an injected process backend, including force-kill success and failure.

## 2026-07-19 external-review Round 1 gate (`v0.9.0-beta.6`)

The 26 HA, state, and process-safety findings in Round 1 were kept as 26
independent commits. `go test -race ./...`, `go vet ./...`, `govulncheck ./...`,
the five-target cross-build, and Linux/Windows arm64/amd64 cross-test compilation
passed before live testing.

| Live gate | Result |
|---|---|
| Rocky and Debian legacy three-line state upgrade | Pass; the fourth database flag was added without fencing either healthy legacy instance |
| Rocky and Debian Remora-only `SIGKILL` | Pass; systemd replacements adopted the unchanged exact Jellyfin PID and rejected duplicates |
| Rocky and Debian normal managed-tree restart | Pass; the old PID exited and exactly one replacement reached `RUNNING` |
| Rocky and Debian manual stop across Remora restart | Pass; both remained `STOPPED` with no PID until explicit `start` |
| Debian NFS loss and recovery (`192.168.1.109:/data`) | Pass; the old PID was fenced and one replacement started after remount |
| Debian SMB loss and recovery (`//192.168.1.106/data`) | Remora pass; fencing, old-PID exit, remount, and single-PID recovery completed. The generic harness must unmount CIFS before installing its drop rule because blocked-server `umount -l` can remain in kernel `D` state until connectivity returns. |
| NFS-hosted live `jellyfin.db` page destruction | Pass; external server-side corruption produced log evidence, process-generation exit confirmed `DATABASE_DAMAGED`, daemon restart preserved the latch, and an explicit repaired start recovered |
| macOS arm64 Remora-only `SIGKILL` | Pass; the replacement adopted the exact Jellyfin PID |
| macOS arm64 required-path disappearance and recovery | Pass; `STORAGE_FENCED` stopped the old PID and recovery started exactly one replacement |
| macOS arm64 manual stop across Remora restart | Pass; no process was revived before explicit `start` |

Live tests used only `remora-review-20260719-round1` directories on NFS and
SMB. Those directories, temporary binaries, transient units, test scripts,
credentials transport, and added test-only packages were removed after the
gate. Both Linux services were returned to one healthy `RUNNING` Jellyfin.

## 2026-07-19 external-review Round 2 gate (`v0.9.0-beta.7`)

The eight Kickstart, archive, and initialization findings in Round 2 were kept
as eight independent commits. `go test -race ./...`, `go vet ./...`,
`govulncheck ./...`, the five-target cross-build, and Linux/Windows arm64/amd64
cross-test compilation passed. One initial full-race run observed the existing
Darwin descendant fixture miss its PID-file deadline; five immediate targeted
runs and the complete repeated race gate passed.

| Isolated local gate | Result |
|---|---|
| Real tar.gz inspection and extraction | Pass; the verified executable was extracted from the selected archive |
| Real ZIP inspection and extraction | Pass; the verified executable was extracted through `zip.NewReader` from the already-open file |
| Missing verification metadata | Pass; extraction failed closed before creating the destination |
| Package path replacement after verification | Pass; extraction continued from the verified open file description rather than the replacement pathname |
| Declared expansion-limit rejection | Pass; oversized extraction left neither destination nor staging directory |
| Injected Kickstart start failure | Pass; generated home, configuration, and launchd artifact were removed in reverse order |
| Injected incomplete rollback | Pass; the error listed each remaining path or service in an exact cleanup manifest |

All Round 2 extraction and rollback gates used Go test-owned isolated temporary
directories. No Jellyfin data, NFS/SMB share, installed service, container, or
virtual machine was modified or left behind.

## 2026-07-19 external-review Round 3 gate (`v0.9.0-beta.8`)

The fifteen local-control, credential-boundary, and observability findings in
Round 3 were kept as fifteen independent commits. `go test -race ./...`,
`go vet ./...`, `govulncheck ./...`, the five-target cross-build, and
Linux/Windows arm64/amd64 cross-test compilation passed.

| Live or deterministic gate | Result |
|---|---|
| macOS socket-only clean initialization | Pass; TCP 8095 stayed closed while the owner-verified Unix socket initialized Jellyfin 10.11.11 and reached `RUNNING` |
| macOS generation-aware credential target | Pass; first-start administrator provisioning and later missing-key recovery completed only while the managed Jellyfin PID owned 8096 |
| macOS Remora crash and API-key recovery | Pass; Remora `SIGKILL` preserved and re-adopted the exact Jellyfin PID, then recreated the deleted key file as mode 0600 |
| macOS concurrent immediate healthchecks | Pass; six concurrent requests returned without blocking control status or the supervisor tick |
| macOS explicit TCP enable | Pass; 8095 appeared only after `tcp-enabled: true`, and loopback status succeeded |
| Debian private runtime socket | Pass; socket-only mode reached Jellyfin 10.11.11 `RUNNING`, created `/run/jellyfin-remora/remora.sock` as mode 0660 `root:jellyfin`, and did not listen on 8095 |
| Debian forged `/tmp` socket | Pass; root `remoractl` ignored a live `.s.remora.8095` owned by the unprivileged Jellyfin account and failed closed |
| Debian explicit TCP enable | Pass; 8095 appeared only with explicit enable and returned the managed status |
| Rotated follow descriptor | Pass in a real temporary-file HTTP follow test; cancellation after replacement closed the current descriptor under race instrumentation |

The macOS test used only `/Users/zhoudingpeng/Appdata/jellyfin` and removed its
contents afterward. Debian used transient `remora-round3*` units and `/tmp`
artifacts; all were removed, and the original `jellyfin-remora.service` was
restored to one healthy Jellyfin 10.11.11 process.

## 2026-07-19 external-review Round 4 gate (`v0.9.0-beta.9`)

The fourteen configuration, platform, and contract-consistency findings in
Round 4 were kept as fourteen independent commits. `go test -race ./...`,
`go vet ./...`, `govulncheck ./...`, the five-target cross-build, and
Linux/Windows arm64/amd64 cross-test compilation passed. The environment parser
also completed its targeted fuzz gate.

| Live or deterministic gate | Result |
|---|---|
| macOS init directory preparation | Pass; non-privileged init validated the physical APFS volume, created all four missing Jellyfin directories, wrote a mode 0600 configuration, and generated a launchd plist |
| macOS repeated init | Pass; `--no-edit` refused implicit replacement, while explicit `--force` wrote a timestamped mode 0600 backup and regenerated the same service artifact |
| macOS configuration permissions | Pass; mode 0644 failed before daemon startup with an owner-only diagnostic, and mode 0600 started with socket-only control |
| macOS environment propagation | Pass; the managed Jellyfin process contained the configured `REMORA_ROUND4_ENV` override while retaining the normal inherited environment |
| macOS ownership-preserving edit | Pass; `edit-config` changed the validated YAML and retained uid 501, gid 20, and mode 0600 across atomic replacement |
| Rocky Linux init and systemd artifact | Pass; unprivileged init created owned data paths, a mode 0600 config and a systemd-analyze-valid unit; forced repeat produced a timestamped backup |
| Debian Linux init and systemd artifact | Pass; the same lifecycle and permission gates passed with the Debian Jellyfin layout and ext4 root volume |
| Linux installed-service isolation | Pass; both pre-existing `jellyfin-remora.service` instances remained active throughout; generated test units were never installed or started |
| Windows platform behavior | Pass in amd64/arm64 cross-build and deterministic Volume API, environment-case, and validation seams; native execution remains a release gate |

All local and remote artifacts used unique `round4` names and were removed.
No installed unit, existing Jellyfin data, NFS/SMB content, container, virtual
machine, or user SSH configuration was changed.
