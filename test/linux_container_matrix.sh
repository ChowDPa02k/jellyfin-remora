#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
arch=${LINUX_TEST_ARCH:-$(go env GOARCH)}
case "$arch" in amd64|arm64) ;; *) echo "unsupported LINUX_TEST_ARCH: $arch" >&2; exit 2 ;; esac
command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }

work=$(mktemp -d "${TMPDIR:-/tmp}/remora-linux-container-test.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
CGO_ENABLED=0 GOOS=linux GOARCH=$arch go test -c -o "$work/platform.test" "$repo/internal/platform"
CGO_ENABLED=0 GOOS=linux GOARCH=$arch go test -c -o "$work/probe.test" "$repo/internal/probe"

# openSUSE Tumbleweed supplies the rolling-distribution syscall baseline. These
# containers verify the native backend on the target kernel ABI; they do not
# replace real systemd, mount-fault, Jellyfin, or reboot tests.
for image in debian:13-slim ubuntu:24.04 fedora:latest opensuse/tumbleweed:latest; do
	echo "linux container matrix: $image ($arch)"
	docker run --rm --platform "linux/$arch" -v "$work:/tests:ro" "$image" \
		/bin/sh -c '/tests/platform.test -test.v && /tests/probe.test -test.v'
done
