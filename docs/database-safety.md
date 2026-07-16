# Jellyfin database damage fence

Jellyfin Remora detects a running Jellyfin process whose primary SQLite
database has become unusable without becoming another SQLite client itself.
It never opens, copies, runs `PRAGMA`, or invokes `sqlite3` against a live
`jellyfin.db`.

## Detection

The detector observes only new bytes already being captured from Jellyfin's
stdout and stderr. It recognizes high-confidence SQLite corruption evidence,
including error 11 (`database disk image is malformed`), error 26 (`file is not
a database`), corruption-at-line reports, and malformed schema reports. ANSI
terminal escapes and arbitrary output chunk boundaries are handled.

Operational failures are deliberately excluded: database locked, read-only,
disk full, uniqueness violations, timeouts, and ordinary HTTP failures do not
mean that database pages are corrupt.

A console signature first marks the database `suspected`. Remora confirms it
only when Jellyfin's `/health` is unhealthy or small authenticated reads of the
users, items, and activity-log APIs receive a server-side failure. Jellyfin
documents that `/health` verifies HTTP and database connectivity. The
additional requests are read-only and go through Jellyfin's own data layer.

```yaml
remora:
  monitoring:
    database:
      enabled: true
      confirmation-window: 5m
      failure-threshold: 1
```

The feature is enabled by default. `confirmation-window` prevents an old,
unconfirmed signature from remaining actionable forever. A larger failure
threshold can debounce an unusually noisy environment, but exact corruption
evidence should normally fail closed on the first API confirmation.

## State and recovery

Confirmed damage performs the following transition:

```text
RUNNING/DEGRADED -> STOPPING -> DATABASE_DAMAGED
```

The process is stopped gracefully and force-terminated only under the existing
stop timeout policy. The fence is written as the fourth line of
`jellyfin.state`, so restarting Remora cannot accidentally restart Jellyfin
against the same damaged database.

Recovery is administrative:

1. Keep Jellyfin stopped.
2. Back up the complete damaged data directory before attempting repair.
3. Restore a known-good backup or follow Jellyfin's supported recovery
   procedure. Do not edit the live database over SMB/NFS.
4. Verify the storage mount and permissions.
5. Run `remoractl start`. This clears the fence as an explicit acknowledgement
   and performs one controlled start. If corruption evidence and API failure
   recur, Remora fences it again.

`remoractl restart` is rejected while `DATABASE_DAMAGED` is latched because a
restart is not a database repair.

## Network storage warning

Media-only shares can reasonably use NFS `nolock` and bounded soft-mount
semantics. Jellyfin's database and application state are different: concurrent
access, caching, disconnect semantics, and missing file locking can corrupt or
invalidate SQLite assumptions. Keep application data on local storage whenever
possible. If it must use network storage, use a protocol and mount policy whose
locking, durability, cache-coherency, and single-writer behavior are explicitly
supported by the storage server and Jellyfin deployment.
