# Contributing to Jellyfin Remora

## Toolchain

The exact minimum Go release is declared by the `go` directive in `go.mod`.
Jellyfin Remora intentionally follows patched Go point releases because it ships a
long-running local service that handles credentials and process control. With
`GOTOOLCHAIN=auto` (the Go default), an older local `go` command downloads and uses
the declared toolchain automatically; contributors do not need a custom compiler.

Verify the selected version before reporting build failures:

```sh
go version
go env GOTOOLCHAIN
```

## Required checks

Before submitting a change, run:

```sh
make check
make test
make vuln
make cross-build
```

Changes to process discovery, storage probes, supervision, configuration migration,
credentials, or control endpoints must also be reviewed against
[`docs/architecture-safety.md`](docs/architecture-safety.md).
