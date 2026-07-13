# Architecture and safety boundaries

This document defines the invariants that must remain true as Jellyfin Remora gains
new platform backends and control-plane features.

## Supervisor invariants

- Desired state is authoritative: an explicit administrative stop always wins over
  storage recovery, health recovery, and restart policy.
- Required storage is checked before Jellyfin starts. A missing mount, mismatched
  source, failed I/O probe, or required write failure fences Jellyfin.
- Fencing stops the supervised process group and prevents restart until every required
  storage check passes the configured consecutive-success threshold.
- Only one Remora may own a control socket and only one matching Jellyfin process may
  be supervised. Ambiguous multiple matches fail closed.
- PID files are advisory. Adoption and termination also require executable and argument
  identity; a PID-file number alone is never sufficient authority to signal a process.
- Startup and ordinary runtime failures use bounded counters and backoff. Opening the
  restart circuit requires an explicit administrative reset.
- Every state-changing API operation is serialized by the supervisor.

## Filesystem trust boundaries

Configured disk targets and Jellyfin paths are operator-controlled input, not proof
that the intended storage is present. Remora therefore validates the live mount table,
expected source identity, permissions, and a timed real I/O probe.

Remora does not create missing Jellyfin data, configuration, cache, or log directories
during normal supervision. This prevents a disappeared `/Volumes` mount from becoming
a new local directory tree. The explicit `validate-config --prepare` operation creates
those directories only after their configured storage has passed validation.

The platform mount backend may recreate a missing mount-point directory before a mount
attempt. It rejects relative paths, the filesystem root, symlinks, and non-directory
targets. Creating mount points below system-owned locations such as `/Volumes` requires
the documented LaunchDaemon privilege.

Temporary write probes use unique files, sync their contents, close them, and remove
them. Probe timeouts are isolated so a blocked filesystem operation cannot stall the
supervisor loop indefinitely. Each probe deliberately runs in a short-lived child
process: a context cannot reliably cancel a kernel-blocked filesystem syscall, while
the isolated process can be terminated without wedging Remora. The per-probe fork is
an explicit availability cost, controlled by each disk's monitoring interval.

Remora-owned state/key writes and Jellyfin XML writes intentionally use different
ownership policies. XML replacement preserves the existing Jellyfin file owner and
mode; Remora state and secrets use daemon-selected restrictive ownership and must not
inherit metadata from an untrusted destination. Shared atomic-write code must retain
that distinction if these implementations are consolidated later.

## Child-process privilege and environment

Jellyfin always runs as a separate operating-system process. Remora inherits the
current environment and does not replace hardware-runtime, CPU, GPU, NPU, library, or
threading variables.

On macOS, a root Remora requires `jellyfin.run-as-user`; running Jellyfin itself as root
is rejected. The child receives the selected account's HOME, USER, LOGNAME, primary
group, and supplementary groups. It runs in a distinct process group so graceful and
forced shutdown apply to Jellyfin and its descendants without signaling unrelated
processes.

Privilege is used for service lifecycle and mounting, not for changing Jellyfin's CPU,
GPU, NPU, NUMA, cgroup, or scheduler policy. Future platform backends must preserve this
separation unless an explicit opt-in resource policy is added.

## Secrets threat model

The current configuration may contain SMB and Jellyfin passwords. Operator
configuration must be owner-only (`0600`); Remora warns when group or other access is
present. Inline SMB credentials are a compatibility mechanism and may be transiently
visible to privileged process inspection on macOS. Keychain-backed credentials remain
required before stable release.

The Remora-owned Jellyfin API key is written atomically with owner-only permissions.
Passwords and API keys must never be logged. Mount errors redact the configured SMB
password, and control endpoints return status rather than credentials.

The REST listener is restricted to syntactic loopback addresses and the Unix socket is
local. Remote control is out of scope for v1; adding it requires TLS, scoped
authentication, authorization tests, and a separate threat-model review. Jellyfin
administrator or watchdog credentials must not be reused as Remora control-plane
credentials.

## Change review checklist

Any change to process discovery, mount handling, state transitions, configuration
migration, credential storage, or control endpoints must answer:

1. Can storage loss create a false local data tree or allow Jellyfin to keep writing?
2. Can PID reuse, duplicate discovery, or a stale state file signal the wrong process?
3. Can a manual stop be overridden by an automatic recovery path?
4. Can an I/O, API, or child-process operation block without a deadline?
5. Can a secret reach logs, command output, process arguments, or remote responses?
6. Does the change alter Jellyfin's inherited runtime or hardware access?
7. Is the failure path covered by a deterministic test and, where safe, a real fault
   test?
