# Jellyfin Remora

Jellyfin Remora is a companion supervisor for Jellyfin. The current macOS milestone supports storage fencing, process supervision, first-run setup, pre-start XML configuration reconciliation, API-key provisioning, login watchdog checks, health checks, and local control.

Development milestones through the cross-platform stable release are tracked in [ROADMAP.md](ROADMAP.md).
The repeatable and real-fault high-availability coverage is recorded in [test/HA_TEST_MATRIX.md](test/HA_TEST_MATRIX.md).
Supervisor invariants and trust boundaries are defined in [docs/architecture-safety.md](docs/architecture-safety.md).
The local control-plane contract is documented in [docs/api-v1.md](docs/api-v1.md).
Build and review requirements are documented in [CONTRIBUTING.md](CONTRIBUTING.md).

## Build

```sh
go build -o build/jellyfin-remora ./cmd/jellyfin-remora
go build -o build/remoractl ./cmd/remoractl
```

The fully annotated Darwin template is
[`sample/config-darwin.yaml`](sample/config-darwin.yaml). For an installed pair
of binaries, run `remoractl init`: it selects the host-platform template, opens
a mode-`0600` temporary copy with `$VISUAL`, `$EDITOR`, `vi`, or `nano`, strictly
validates the saved YAML, and atomically writes it to
`jellyfin.config-dir/config.yaml`. On macOS it also generates a launchd plist in
that directory using the actual binary and configuration paths. The command
does not bootstrap the plist automatically. Linux systemd and Windows Task
Scheduler generators are reserved stubs until their platform phases; SysVinit
will not be supported.

Replace the template's volume UUID, accounts, and credentials, and create all
four Jellyfin directories with ownership and write permission for the selected
user. Remora deliberately does not create missing data directories because
doing so beneath a lost `/Volumes` mount could create a false local data tree.
When Remora runs as root, `jellyfin.run-as-user` is mandatory and Jellyfin is
started with that account.

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
./build/remoractl init
./build/jellyfin-remora validate-config -c /path/to/jellyfin/config/config.yaml
# Optional: create missing directories only after their configured storage passes validation.
./build/jellyfin-remora validate-config -c /path/to/jellyfin/config/config.yaml --prepare
./build/jellyfin-remora -c /path/to/jellyfin/config/config.yaml
./build/remoractl status
./build/remoractl healthcheck
```

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
state conflicts, and operation timeouts. API-key management, log querying,
configuration editing, session termination, diagnostic bundles, and metrics are
planned for later `v0.4.0-alpha` iterations.

### macOS tarball installations

Remora also supports the portable macOS Jellyfin archive layout, where the
lowercase `jellyfin` executable and `jellyfin-web` directory are siblings. Set
`jellyfin.path` to the extracted directory and `jellyfin.web-dir` to its
`jellyfin-web` child. Preserve the executable bit when extracting. On current
macOS releases, a downloaded, unsigned archive may additionally require local
ad-hoc signing after its checksum has been verified:

```sh
xattr -dr com.apple.quarantine /absolute/path/to/jellyfin-10.10.7
codesign --force --sign - /absolute/path/to/jellyfin-10.10.7/jellyfin
```

On Darwin, `validate-config` reports this attribute and the daemon emits a WARN
record with the executable path before supervision begins. Detection is
advisory: Remora does not remove metadata, bypass Gatekeeper, or refuse startup.

Command-line parameters are Jellyfin-version-specific. Jellyfin 10.10.7 hosts
the configured `--webdir` by default and rejects `--hostwebclient`; a compatible
optional parameter is `package-name: jellyfin-remora-tar`.

The REST listener accepts loopback addresses only. `remoractl` uses `/tmp/jellyfin-remora.sock` by default and accepts `--host http://127.0.0.1:8095` as a fallback.

## launchd

Install the two binaries and configuration at the paths in `packaging/io.github.chowdpa02k.jellyfin-remora.plist`, then install the LaunchDaemon:

```sh
sudo cp packaging/io.github.chowdpa02k.jellyfin-remora.plist /Library/LaunchDaemons/
sudo chown root:wheel /Library/LaunchDaemons/io.github.chowdpa02k.jellyfin-remora.plist
sudo chmod 0644 /Library/LaunchDaemons/io.github.chowdpa02k.jellyfin-remora.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/io.github.chowdpa02k.jellyfin-remora.plist
```

SMB passwords in YAML are supported for the first milestone but can be visible transiently to privileged process inspection. Keep the configuration `0600`; Keychain-backed credentials are planned for a later milestone.

## Safety rules

- A missing mount, wrong mount source, or failed required I/O probe fences Jellyfin.
- A reachable mount with working I/O is only degraded when its SMB/NFS port probe fails.
- Storage must pass three consecutive checks before automatic recovery.
- Manual stop always overrides automatic recovery.
- Five process failures in ten minutes open the restart circuit; `remoractl start` resets it.

## Development checks

```sh
make build
make test
make check
make vuln
make cross-build
```

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
