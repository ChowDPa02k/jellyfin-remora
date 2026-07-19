# Compatibility policy

This document freezes the first Jellyfin Remora beta compatibility baseline.
It applies from `v0.9.0-beta.1` through the `v0.9.x` beta series. The canonical
machine-readable counterpart is
[`schema/compatibility-v0.9.json`](../schema/compatibility-v0.9.json).

## Change classes

Compatible changes may add optional configuration-independent behavior, JSON
response fields, enum values, diagnostics, and commands. Clients must ignore
unknown JSON fields and display unknown enum values. Human-oriented table and
log formatting may improve without notice; `--json`, documented exit codes,
and API response types are the automation contract.

A change is incompatible if it removes or renames a configuration field, route,
JSON field, service identity, or stable filesystem location; changes a field's
type or meaning; reassigns an exit code; or makes an already accepted state file
unreadable. Such a change requires a new config or API major version, an
explicit migration, release notes, and a deprecation window. It cannot be made
silently within `v0.9.x`.

## Configuration schema v2

- `config-version: 2` is the current writable schema. Unknown fields fail
  closed, so spelling mistakes never silently weaken storage fencing.
- Legacy unversioned and v1 files remain accepted and migrate to v2 in memory.
  Remora does not rewrite an operator's YAML automatically.
- Versions greater than 2 are rejected. A future schema must ship an explicit
  migration and cannot reinterpret a v2 field.
- Platform validation remains intentional: a field can be valid in the schema
  but rejected on an OS that cannot implement it safely. Linux SMB credentials
  accept `file:/absolute/path`, an absolute path, or `libsecret:NAME`; Windows
  accepts `windows-credential-manager`; macOS rejects credential providers.
- `v0.9.0-beta.3` adds optional `jellyfin.env` string overrides. The child first
  inherits Remora's complete environment, then replaces only configured names;
  omission therefore preserves all earlier process and hardware-runtime behavior.
- `v0.9.0-beta.4` makes lifecycle-operation acknowledgement conditional on a
  successful durable state write. A failed write rejects and rolls back the
  in-memory start/stop/restart mutation, then best-effort rewrites the previous
  intent to both state locations so a one-shot partial write cannot be replayed.
- `v0.9.0-beta.7` makes repeated interactive `remoractl init` edit the existing
  configuration and create an owner-only timestamped backup. Unattended
  replacement now requires the explicit `--no-edit --force` combination.

## Kickstart package and installation safety

From `v0.9.0-beta.7`, every extracted Generic package requires a verified
SHA-256 digest and size. Verification and extraction use the same open file
description; each expanded file is limited to 512 MiB, the complete expansion
to 4 GiB, and the archive to 100,000 entries. Repository rate limits, server
failures, and timeouts remain repository errors rather than package-absence
results. A failed deployment rolls back completed installation steps in reverse
order and reports an exact cleanup manifest if rollback cannot finish.

## Local REST API v1 and CLI

API v1 operations, response evolution rules, error envelope, stable error
codes, and headers are defined in [api-v1.md](api-v1.md). Routes are never
repurposed within v1. New response fields and enum values are additive.

`remoractl` exit codes are frozen as follows: 0 success, 1 unclassified local
failure, 2 usage or invalid request, 3 unavailable daemon/transport/server, 4
safety or state conflict, and 5 lifecycle convergence timeout. Scripts should
use exit status and `--json`, not parse the pretty tables.

## Durable and runtime files

| File | Frozen format and rule |
|---|---|
| `jellyfin.state` | The frozen three-line prefix is health, storage damage, and manual stop. `v0.9.0-beta.2` adds a fourth database-damage flag; older readers ignore it and newer readers treat a missing fourth line as clear. Further trailing lines remain ignored. Truncated or malformed content never enables a fence. Mode `0640`. |
| `jellyfin.pid` | Decimal PID plus newline. It is advisory only; Remora verifies executable and arguments before adoption or signalling. Mode `0640`. |
| `.remora_api_key` | Opaque token plus newline. It is preserved across upgrades and never exposed through diagnostics. Mode `0600`. |

Root daemons keep the runtime state beneath `/var/run/jellyfin-remora/<id>`;
non-root daemons use the owner-specific temporary directory. The durable copies
remain under `remora.data-dir`. Fatal storage state is not written through to a
possibly unsafe durable volume.

Database-damage detection is intentionally indirect: Remora never opens the
live SQLite file. It combines new Jellyfin console corruption evidence with
Jellyfin's own health or authenticated read-only API result. A confirmed flag
survives daemon restart. Repair or restore the database while Jellyfin is
stopped, then use `remoractl start` as the explicit acknowledgement.

## Frozen platform identities

| Platform | Service/control identity | Packaged locations |
|---|---|---|
| macOS arm64 | launchd label `io.github.chowdpa02k.jellyfin-remora` | plist under `/Library/LaunchDaemons`; launchd stdout/stderr under `/var/log/jellyfin-remora.launchd.*` |
| Linux amd64/arm64 | `jellyfin-remora.service`; `/run/jellyfin-remora/remora.sock` | config `/etc/jellyfin-remora/remora-config.yaml`; binaries `/usr/bin`; state `/var/lib/jellyfin-remora`; logs `/var/log/jellyfin-remora` |
| Windows amd64 | service `JellyfinRemora`; task `JellyfinRemora-User`; pipe `\\.\pipe\jellyfin-remora` | Installer-selected Program Files and ProgramData roots remain upgrade-preserved |

Intel macOS remains explicitly unsupported. Windows arm64 is build-only until
its native compatibility gate passes and is therefore not part of this frozen
release-target set.

## Upgrade and rollback rules

- In-place upgrades preserve operator configuration, durable state, API keys,
  Jellyfin data, and service identity. Packages must not replace a modified
  config file.
- An upgrade may adopt the verified existing Jellyfin process; it must never
  launch a duplicate. Storage fencing and manual stop remain authoritative.
- No upgrade automatically rewrites configuration or state into a format the
  previous beta cannot read.
- Rollback is supported only while the target version can read the retained
  config and state. Release notes must call out the first release that changes
  this condition and provide a backup/migration procedure.
- Jellyfin's own database downgrade rules are separate from Remora's. Remora
  does not claim to make a Jellyfin data directory backward-compatible.

Every compatibility-surface change must update the machine-readable manifest,
its regression tests, this policy where applicable, and release notes. A test
failure caused by contract drift is a release blocker, not a snapshot to update
without review.
