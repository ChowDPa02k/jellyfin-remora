# Jellyfin Remora

Jellyfin Remora is a companion supervisor for Jellyfin. The current macOS milestone supports storage fencing, process supervision, first-run setup, pre-start XML configuration reconciliation, API-key provisioning, login watchdog checks, health checks, and local control.

Development milestones through the cross-platform stable release are tracked in [ROADMAP.md](ROADMAP.md).
The repeatable and real-fault high-availability coverage is recorded in [test/HA_TEST_MATRIX.md](test/HA_TEST_MATRIX.md).
Supervisor invariants and trust boundaries are defined in [docs/architecture-safety.md](docs/architecture-safety.md).

## Build

```sh
go build -o build/jellyfin-remora ./cmd/jellyfin-remora
go build -o build/remoractl ./cmd/remoractl
```

Copy `config.example.yml`, replace its volume UUID and macOS user, and create all four Jellyfin directories with ownership and write permission for that user. Remora deliberately does not create missing data directories because doing so beneath a lost `/Volumes` mount could create a false local data tree. When Remora runs as root, `jellyfin.run-as-user` is mandatory and Jellyfin is started with that account.

For a new Jellyfin data directory, configure `init.user` and `init.password`. Remora completes the setup wizard, handles the macOS package's OS-account bootstrap user on Jellyfin 10.11 and 12, renames it to the configured administrator, creates a `Jellyfin Remora` API key with mode `0600`, creates the optional login-watchdog user, and performs a controlled restart. Existing initialized servers are not sent through the setup wizard.

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
./build/jellyfin-remora validate-config -c config.yml
# Optional: create missing directories only after their configured storage passes validation.
./build/jellyfin-remora validate-config -c config.yml --prepare
./build/jellyfin-remora -c config.yml
./build/remoractl status
./build/remoractl healthcheck
```

`remoractl status` and the converged results of lifecycle commands use
go-pretty Unicode-aware tables for process identity, Jellyfin server metadata,
storage, and active sessions. The active-sessions table is omitted when no
clients are active; otherwise its rows distinguish `playing`, `paused`, and
`idle` clients without exposing access tokens. Use either `remoractl --json status` or
`remoractl status --json` for the additive, machine-readable `/v1/status`
document. Older clients safely ignore the new status fields.

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

API-key rotation/revocation commands, log querying, and session control remain future milestones.

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
strict validation. An unversioned legacy file is treated as version 0 and migrated to
version 1 without rewriting the source file; conflicting legacy and current keys fail
closed.

## License

Jellyfin Remora is available under the [MIT License](LICENSE).
