# Local control API v1

Jellyfin Remora API v1 is a local control-plane contract. The daemon listens on
an owner-restricted Unix socket by default and may additionally listen on an
explicit loopback address. Remote control is not supported by v1.

## Compatibility

Within API major version 1, response objects are additive: clients must ignore
unknown fields. Existing fields do not change meaning or JSON type. New enum
values may be added, so clients should display unknown values rather than reject
an otherwise valid response. The successful `/v1/status` and lifecycle response
shape remains directly decodable by clients shipped before `v0.4.0-alpha.1`.

Every response includes:

- `X-Remora-API-Version: 1`
- `X-Remora-Operation-ID: op-<16 hexadecimal digits>`
- `Content-Type: application/json`

The operation ID identifies one HTTP request and is also returned in structured
error bodies. It is process-local diagnostic metadata, not an authentication
token or a durable distributed identifier.

## Operations

| Method | Path | Success | Description |
|---|---|---:|---|
| `GET` | `/v1/status` | `200` | Current supervisor, process, storage, health, and session status |
| `GET` | `/v1/events?limit=50` | `200` | Most recent state transitions; `limit` must be 1–256 |
| `GET` | `/v1/logs?source=remora&lines=200` | `200` | Bounded tail of the Remora or newest Jellyfin log |
| `GET` | `/v1/config` | `200` | Active configuration path and SHA-256 for conflict-aware local editing |
| `GET` | `/v1/diagnostics` | `200` | Redacted structured local diagnostic bundle |
| `GET` | `/v1/apikeys` | `200` | Jellyfin API keys represented by non-secret hash identifiers |
| `POST` | `/v1/apikeys` | `201` | Create a Jellyfin API key from `{"name":"..."}` |
| `DELETE` | `/v1/apikeys/{id}` | `200` | Revoke one non-Remora API key by an unambiguous ID prefix |
| `GET` | `/v1/sessions` | `200` | Refresh and return active Jellyfin sessions |
| `POST` | `/v1/sessions/{id}/stop` | `200` | Send Jellyfin's `Stop` playstate command to a session |
| `POST` | `/v1/start` | `202` | Request desired running state and reset restart circuits |
| `POST` | `/v1/stop?force=false` | `202` | Request desired stopped state; optionally force process termination |
| `POST` | `/v1/restart?force=false` | `202` | Request one controlled replacement; optionally force termination |
| `POST` | `/v1/healthcheck` | `200` | Refresh Jellyfin health immediately |

Lifecycle requests are asynchronous. Their accepted response is the status at
submission time; `remoractl` subsequently polls status until the requested state
converges or its five-minute operation deadline expires.

## Status states

The `state` field may contain:

- `INIT`, `PREFLIGHT`, `STOPPED`, `STARTING`, `FIRST_START`, `RUNNING`
- `DEGRADED`, `STOPPING`, `RESTART_BACKOFF`
- `STORAGE_FENCED`, `DATABASE_DAMAGED`, `PROCESS_FAILED`

`desired_state` is `running` or `stopped`. Storage safety overrides desired
availability: a daemon in `STORAGE_FENCED` does not start Jellyfin until required
storage has recovered. `manual_stop` prevents automatic recovery from overriding
an operator stop.

`DATABASE_DAMAGED` is a durable safety fence. It requires a high-confidence
SQLite corruption signature from newly captured Jellyfin console output plus a
failed Jellyfin health or authenticated database-backed API probe. Remora stops
Jellyfin and does not automatically restart it. After repairing or restoring
the database, `POST /v1/start` explicitly acknowledges and clears the fence;
`restart` is rejected while it remains latched.

The status document contains additive process identity and resource fields,
storage results (including `latency_ms`), Jellyfin health, active sessions,
`playing_users`, Darwin managed-process-tree `ffmpeg_processes`, session-derived
`active_transcodes`, additive database `damaged`/`suspected` evidence, and
transition/error metadata. Timestamps use RFC 3339 JSON time
encoding. Zero or unavailable optional values may be omitted.

## Management safety

Log reads accept `source=remora|jellyfin` and `lines=1..2000`, scan at most the
last 4 MiB, and reject symlinks or non-regular files. API-key responses contain a
16-hex-character SHA-256-derived ID rather than the access token. At least eight
ID characters are required for deletion, ambiguous prefixes fail closed, and the
credential currently used by Remora cannot revoke itself.

`remoractl edit-config` uses `GET /v1/config` to identify the active file and its
checksum, edits an owner-only temporary copy, parses it through the same strict
configuration loader, checks that the source did not change while the editor was
open, and atomically writes mode `0600`. The daemon must be restarted to load the
new configuration. Configuration contents are never returned by the API.

`GET /v1/diagnostics` contains build identity, status, the complete bounded event
ring, a non-secret configuration summary, and a bounded Remora log tail. It does
not contain YAML configuration contents or API-key access tokens. `remoractl
diagnose --output FILE` writes the JSON bundle with mode `0600`.

## Events

`GET /v1/events` returns:

```json
{
  "events": [
    {
      "sequence": 42,
      "timestamp": "2026-07-14T10:00:00+08:00",
      "type": "state_transition",
      "state": "RUNNING"
    }
  ]
}
```

Sequences increase for the lifetime of one daemon process. The in-memory ring
retains at most 256 events and is intentionally reset when the daemon restarts.

## Errors

API-owned failures use this envelope:

```json
{
  "error": {
    "code": "storage_fenced",
    "message": "required storage is unsafe",
    "operation_id": "op-000000000000002a"
  }
}
```

Current stable codes are:

| Code | HTTP status | Meaning |
|---|---:|---|
| `invalid_argument` | `400` | A query or request value is invalid |
| `not_found` | `404` | No v1 operation exists at the path |
| `method_not_allowed` | `405` | The path exists but does not support the HTTP method |
| `log_unavailable` | `404` | The selected safe log file is unavailable |
| `config_unavailable` | `404` | The active configuration cannot be inspected |
| `storage_fenced` | `409` | Storage safety prevents the requested start |
| `database_damaged` | `409` | Confirmed database damage prevents restart until an explicit repaired start |
| `operation_rejected` | `400` | The supervisor rejected the requested operation |
| `follow_limit_reached` | `429` | The bounded concurrent log-follow limit was reached |
| `jellyfin_error` | `502` | Jellyfin could not complete a management request |

Clients should primarily branch on `code`, retain the operation ID for logs, and
treat the human-readable message as diagnostic text rather than a stable value.

## remoractl exit codes

| Exit | Meaning |
|---:|---|
| `0` | Command completed successfully |
| `1` | Local/internal failure not classified below |
| `2` | Usage error or invalid/not-found API request |
| `3` | Daemon unavailable or server-side/API transport failure |
| `4` | Requested operation conflicts with current safety/state constraints |
| `5` | Accepted lifecycle operation did not converge before the deadline |

All daemon-facing `remoractl` commands map to the operations above. `init` is an
interactive local installation workflow that writes `$PWD/remora-config.yaml`,
validates configured storage, and installs or emits the platform service
definition; `edit-config` intentionally performs the final
filesystem replacement locally after using `/v1/config` for daemon-authoritative
path and concurrency metadata.
