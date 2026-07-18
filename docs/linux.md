# Native Linux operation

Native Linux support is for bare-metal or VM Jellyfin installations managed by
systemd. Remora does not impose cgroup CPU, memory, GPU, device, or NUMA limits;
the Jellyfin child retains the host's normal hardware-acceleration environment.
SysV init is not supported.

`jellyfin.env` can make proxy, driver, or vendor-runtime values deterministic
under systemd. It overlays the inherited service environment rather than replacing
it. The Linux sample enables a localhost proxy example; remove its `HTTP_PROXY`,
`HTTPS_PROXY`, and `ALL_PROXY` entries unless a proxy is actually listening on the
configured address. Keep the configuration mode `0600` if values contain secrets.

## Install and initialize

`jellyfin-remora` and `remoractl` must be in the same directory. The portable
tarball and native packages both preserve this invariant. Run `remoractl init`
as root from the directory that should contain `remora-config.yaml`.

For a portable extraction, that can be an application-owned directory:

```sh
cd /opt/jellyfin-remora
sudo /opt/jellyfin-remora/remoractl init
```

After installing a `.deb` or `.rpm`, use the package unit's conventional
configuration directory:

```sh
sudo install -d -m 0750 /etc/jellyfin-remora
cd /etc/jellyfin-remora
sudo /usr/bin/remoractl init
```

The packages install but do not invent an operator configuration or start an
unconfigured service. Upgrade/rollback restarts an already active service;
package removal stops and disables it while preserving the configuration,
Remora state, logs, and Jellyfin data. Portable tarballs, DEBs, and RPMs each
ship with a sibling `.sha256` file; verify it from the artifact directory with
`sha256sum -c <artifact>.sha256` before installation.

Init opens the embedded `config-linux.yaml`, validates device identity and real
read/write access, creates Jellyfin directories as the configured run-as user,
installs `/etc/systemd/system/jellyfin-remora.service` idempotently, and asks
before starting it. The generated service conflicts with the distribution's
`jellyfin.service`; do not enable both supervisors.

Package executable paths differ by family:

| Family | Typical executable | Web client |
|---|---|---|
| Debian/Ubuntu | `/usr/lib/jellyfin/bin/jellyfin` | `/usr/share/jellyfin-web` |
| Fedora/RHEL family | `/usr/lib64/jellyfin/jellyfin` | `/usr/share/jellyfin-web` |

Point at the ELF executable rather than `/usr/bin/jellyfin` shell wrappers so
kernel executable identity remains exact during process adoption.

## systemd lifecycle

The service runs Remora in the foreground as root so it can mount storage and
drop only the Jellyfin child to `jellyfin.run-as-user/group`. systemd creates
the runtime, state, and log directories. `KillMode=process` intentionally keeps
Jellyfin alive after an unexpected Remora crash; the restarted daemon adopts
the exact executable and argument set. A normal `systemctl stop` still asks
Remora to stop the complete Jellyfin process tree before exiting. Both the
packaged and init-generated units use `ConditionPathExists` so an accidentally
removed configuration cannot create a restart loop.

```sh
systemctl status jellyfin-remora
journalctl -u jellyfin-remora -f
remoractl --socket /run/jellyfin-remora/remora.sock status
```

## Storage and credentials

Physical filesystems should use filesystem UUIDs instead of unstable `/dev/sdX`
names. Remora parses `/proc/self/mountinfo`, resolves the UUID symlink to its
block device, and refuses a different source at the configured target.

SMB passwords and users are rejected in YAML. Use a root-only credential file:

```text
username=jellyfin
password=replace-me
domain=WORKGROUP
```

```yaml
credential: file:/etc/jellyfin-remora/media.credential
options: vers=3.1.1,seal,uid=jellyfin,gid=jellyfin,forceuid,forcegid,file_mode=0600,dir_mode=0700
```

The file must be regular, owned by the Remora service uid (normally root), and
inaccessible to group/other; symbolic links are rejected. Remora pins the
validated inode and gives `mount.cifs` a read-only inherited descriptor, so a
path replacement cannot race the permission check. Alternatively,
`credential: libsecret:media` invokes:

```sh
secret-tool lookup service jellyfin-remora credential media
```

The secret value must contain the same `username=` and `password=` lines. It is
passed to `mount.cifs` through an anonymous in-memory file descriptor, not a
command-line password or persistent temporary file.

IPv6 network-storage sources must use brackets around the literal address, for
example `//[2001:db8::20]/media` for SMB or
`[2001:db8::30]:/exports/media` for NFS. This keeps source identity and the
bounded TCP reachability probe unambiguous.

For media-only NFSv3, `nolock,soft,timeo=50,retrans=2` bounds failure detection
and avoids lock-manager dependency. Use `hard` semantics for database or shared
application state where retry-until-recovery is more important than bounded
fencing latency. Explicit user options take precedence.

## Failure behavior

While Jellyfin is running, Remora never mounts a replacement filesystem over a
path whose mount disappeared. It first fences and stops Jellyfin; only the
stopped/fenced recovery path may remount. Recovery requires the configured
number of consecutive healthy checks before a new process starts. `/proc`
process state exposes zombies and `D` waits, pidfds avoid PID-reuse races where
available, and cgroup v2 membership supplements ancestry for ffmpeg accounting
and forced cleanup without adding resource limits. Path probes and mount helpers
also honor `io-timeout` without waiting indefinitely for a child stuck in a
kernel `D` wait. A still-blocked helper is retained for eventual reaping and the
same target is not probed or mounted again, preventing an unbounded process or
goroutine pile-up during a stale-filesystem incident.
