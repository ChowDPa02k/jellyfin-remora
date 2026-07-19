# Kickstart zero-knowledge deployment

`remoractl kickstart` is the guided alternative to the expert-oriented `init`
workflow. It uses a Bubble Tea TUI and produces a strict
`remora-config.yaml` plus the native service definition for the host platform.

## Interactive flow

Keep `remoractl` and `jellyfin-remora` together, then run:

```sh
sudo ./remoractl kickstart
```

Kickstart performs these steps:

1. It detects a compatible Jellyfin application or native package. macOS
   Applications, Debian/RPM/Arch-style Linux layouts, and Windows Program
   Files are recognized. The detected binary is checked against the current OS
   and architecture.
2. If the detected installation is declined, it accepts a Generic `.tar.gz`,
   `.tar.xz`, or `.zip`. Archive entries are checked for path traversal and the
   contained executable must match the current OS and architecture before any
   extraction occurs. Kickstart then verifies the selected file against the
   official Jellyfin repository. It prefers the repository's published SHA-256;
   for legacy archive-only releases without checksum metadata, it downloads the
   equally sized official package and compares hashes. A package that cannot be
   verified is not installed. The archive fallback covers stable, preview, and
   date-versioned unstable packages.
3. It asks for Jellyfin home. Deployment creates `data`, `config`, `cache`,
   `logs`, and `transcode` below that directory and adds the containing
   filesystem to the storage watchdog with read/write probes.
4. It accepts any number of media paths. Press Enter to add a path and Ctrl+D
   when done. The longest containing physical, SMB/CIFS, or NFS mount is
   inferred, deduplicated, and added with read-only probes.
5. It asks for the server name, display language, metadata language, metadata
   region, and administrator password. Language and region choices come from a
   catalog generated from the real Jellyfin API; the YAML stores the exact
   labels displayed by Jellyfin Web.
6. Submit validates storage and generated YAML, creates directories, extracts
   a selected Generic package, writes the configuration atomically, and
   installs and starts launchd or systemd when privileges permit.

Deployment is transactional. If a later step fails, Kickstart reverses completed
filesystem and service-installation steps. Existing files are restored. If any
rollback action fails, the error prints an exact `cleanup required` manifest;
each listed path or service is the remaining operator action.

Without administrator privileges, Kickstart still writes the configuration
and service artifact in the current directory, then prints exact manual install
instructions. It does not silently elevate privileges.

## Generated defaults

Kickstart deliberately emits a small configuration:

- Jellyfin uses its default port, and Remora uses loopback port `8095` and the
  platform local control endpoint.
- The login watchdog user is `remora`; its password is a randomly generated
  16-character alphanumeric value.
- `jellyfin.branding.login-disclaimer` is `Powered by Jellyfin Remora`.
- `jellyfin.parameters`, `jellyfin.general`,
  `jellyfin.general.performance`, and `jellyfin.networking` are absent rather
  than empty mappings. Jellyfin retains ownership of those settings.
- The initial administrator username is `admin`.

The generated file contains both administrator and watchdog credentials and is
written with mode `0600` on Unix. Treat it as a secret.

On privileged Linux deployments, Kickstart copies the sibling binaries to
`/usr/local/bin` before generating the unit. This gives systemd a stable path
and the correct SELinux executable context. Repeated deployment replaces those
files atomically, reloads systemd, clears a prior start-limit failure, and
restarts the service.

## Non-interactive acceptance tests

Automation can provide the same answers in YAML:

```yaml
use-detected: true
jellyfin-home: /var/lib/jellyfin-kickstart
media-paths:
  - /mnt/media
server-name: Home Cinema
display-language: Deutsch
metadata-language: German
metadata-region: Germany
admin-password: replace-this-secret
```

```sh
sudo remoractl kickstart --answers ./kickstart-answers.yaml
```

Use `archive: /path/to/jellyfin.tar.xz` with `use-detected: false` for a
Generic package. `--no-start` installs or generates the service without
starting it. The answer-file mode is intended for repeatable validation and
provisioning; the TUI remains the normal user path.

## Repository verification and network timeouts

The interactive validation screen uses a spinner and always exposes its active
network phase as `Connecting Jellyfin repo` or `Downloading package`. The
answer-file mode prints the same phase text to standard error, so package
verification never appears to hang silently.

Repository access uses bounded DNS/TCP and TLS connection timeouts, bounded
response-header/request timeouts, and a bounded whole-package download timeout.
Rate limits, server failures, and timeouts are reported as repository errors;
they are never presented as evidence that the selected package does not exist.
The legacy download path also limits the response to the selected package's
known size. After a successful online comparison, Kickstart hashes the selected
file again immediately before extraction and rejects it if its size or content
changed. Proxy discovery continues to use Go's standard environment behavior
(`HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY`).

## macOS Generic packages and Gatekeeper

Kickstart never bypasses Gatekeeper. A downloaded unsigned archive can retain
`com.apple.provenance` after extraction, in which case Remora logs a warning
and macOS may terminate the executable. After checking the archive checksum,
an administrator can explicitly ad-hoc sign the extracted bundle:

```sh
codesign --force --deep --sign - "$JELLYFIN_HOME/server/jellyfin/jellyfin"
```

The `--deep` option is needed by the tested Jellyfin 10.10.7 Generic archive
because its executable directory contains runtime subcomponents.

## Validated platforms

The complete flow has been exercised with:

| Host | Jellyfin | Installation | Storage | Result |
|---|---:|---|---|---|
| macOS arm64 | 12.0.0 | Applications | APFS + existing SMB | Passed |
| macOS arm64 | 10.10.7 | Generic `.tar.xz` | APFS | Passed after explicit Gatekeeper signing |
| Rocky Linux 10.1 x86_64 | 10.11.11 | RPM | XFS | Passed with SELinux enforcing |
| Debian 13 x86_64 | 10.11.11 | DEB | local + existing NFS + CIFS | Passed |

The generated Windows service artifact and Windows builds are covered by
cross-platform unit/build checks; a native Windows Kickstart end-to-end run is
still pending.
