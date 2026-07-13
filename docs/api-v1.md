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
- `STORAGE_FENCED`, `PROCESS_FAILED`

`desired_state` is `running` or `stopped`. Storage safety overrides desired
availability: a daemon in `STORAGE_FENCED` does not start Jellyfin until required
storage has recovered. `manual_stop` prevents automatic recovery from overriding
an operator stop.

The status document contains additive process identity and resource fields,
storage results (including `latency_ms`), Jellyfin health, active sessions,
`playing_users`, and transition/error metadata. Timestamps use RFC 3339 JSON time
encoding. Zero or unavailable optional values may be omitted.

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
| `storage_fenced` | `409` | Storage safety prevents the requested start |
| `operation_rejected` | `400` | The supervisor rejected the requested operation |

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
