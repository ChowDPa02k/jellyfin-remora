#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
arch=${LINUX_TEST_ARCH:-$(go env GOARCH)}
case "$arch" in amd64|arm64) ;; *) echo "unsupported LINUX_TEST_ARCH: $arch" >&2; exit 2 ;; esac
command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }

work=$(mktemp -d "${TMPDIR:-/tmp}/remora-linux-container-test.XXXXXX")
container_timeout=${LINUX_CONTAINER_TIMEOUT:-300}
case "$container_timeout" in ''|*[!0-9]*|0) echo "invalid LINUX_CONTAINER_TIMEOUT: $container_timeout" >&2; exit 2 ;; esac
current_container=
cleanup() {
	if [ -n "$current_container" ]; then
		docker rm -f "$current_container" >/dev/null 2>&1 || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM
CGO_ENABLED=0 GOOS=linux GOARCH=$arch go test -c -o "$work/platform.test" "$repo/internal/platform"
CGO_ENABLED=0 GOOS=linux GOARCH=$arch go test -c -o "$work/probe.test" "$repo/internal/probe"

run_container() {
	image=$1
	sequence=$2
	current_container="remora-linux-matrix-$$-$sequence"
	timed_out="$work/timed-out-$sequence"
	docker run --rm --name "$current_container" --platform "linux/$arch" \
		-v "$work:/tests:ro" "$image" \
		/bin/sh -c '/tests/platform.test -test.v && /tests/probe.test -test.v' &
	client=$!
	(
		sleep "$container_timeout"
		if kill -0 "$client" >/dev/null 2>&1; then
			echo "linux container matrix timed out after ${container_timeout}s: $image ($arch)" >&2
			: > "$timed_out"
			docker rm -f "$current_container" >/dev/null 2>&1 || true
			kill -TERM "$client" >/dev/null 2>&1 || true
		fi
	) &
	watchdog=$!
	set +e
	wait "$client"
	rc=$?
	kill "$watchdog" >/dev/null 2>&1
	wait "$watchdog" >/dev/null 2>&1
	set -e
	current_container=
	if [ -f "$timed_out" ]; then
		return 124
	fi
	return "$rc"
}

# openSUSE Tumbleweed supplies the rolling-distribution syscall baseline. These
# containers verify the native backend on the target kernel ABI; they do not
# replace real systemd, mount-fault, Jellyfin, or reboot tests.
sequence=0
for image in debian:13-slim ubuntu:24.04 fedora:latest opensuse/tumbleweed:latest; do
	sequence=$((sequence + 1))
	echo "linux container matrix: $image ($arch)"
	run_container "$image" "$sequence"
done
