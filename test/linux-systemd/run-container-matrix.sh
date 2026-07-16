#!/bin/sh
set -eu

# Run real Jellyfin under native systemd on an Ubuntu LTS and an openSUSE
# Tumbleweed container. This is intentionally destructive and requires a
# privileged disposable Podman host with loop-device support.

usage() {
	cat >&2 <<'EOF'
usage: run-container-matrix.sh REMORA-DEB REMORA-RPM JELLYFIN-SERVER-DEB JELLYFIN-WEB-DIR
EOF
	exit 2
}

[ "$#" -eq 4 ] || usage
remora_deb=$(realpath "$1")
remora_rpm=$(realpath "$2")
jellyfin_deb=$(realpath "$3")
jellyfin_web=$(realpath "$4")

root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
podman=${PODMAN:-podman}
prefix=${REMORA_MATRIX_PREFIX:-remora-systemd-matrix}
work=$(mktemp -d "${TMPDIR:-/tmp}/remora-systemd-matrix.XXXXXX")
active_containers=
active_loops=

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }
for command in "$podman" ar losetup realpath tar; do
	command -v "$command" >/dev/null 2>&1 || { echo "required command not found: $command" >&2; exit 1; }
done
for file in "$remora_deb" "$remora_rpm" "$jellyfin_deb"; do
	[ -f "$file" ] || { echo "file not found: $file" >&2; exit 1; }
done
[ -d "$jellyfin_web" ] || { echo "Jellyfin Web directory not found: $jellyfin_web" >&2; exit 1; }

cleanup() {
	for container in $active_containers; do
		"$podman" rm -f "$container" >/dev/null 2>&1 || true
	done
	for loop in $active_loops; do
		losetup -d "$loop" >/dev/null 2>&1 || true
	done
	rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM

data_member=$(ar t "$jellyfin_deb" | awk '/^data\.tar\./ { print; exit }')
[ -n "$data_member" ] || { echo "Jellyfin DEB has no data archive" >&2; exit 1; }
mkdir -p "$work/jellyfin-root"
ar p "$jellyfin_deb" "$data_member" > "$work/$data_member"
tar -xf "$work/$data_member" -C "$work/jellyfin-root"
jellyfin_bin="$work/jellyfin-root/usr/lib/jellyfin/bin"
[ -x "$jellyfin_bin/jellyfin" ] || { echo "Jellyfin executable missing from DEB" >&2; exit 1; }

"$podman" build -t "$prefix-ubuntu:24.04" -f "$root/test/linux-systemd/Containerfile.ubuntu" "$root"
"$podman" build -t "$prefix-opensuse:tumbleweed" -f "$root/test/linux-systemd/Containerfile.opensuse" "$root"

run_case() {
	distro=$1
	image=$2
	package=$3
	container="$prefix-$distro-$$"
	loop=$(losetup -f)
	active_containers="$container $active_containers"
	active_loops="$loop $active_loops"

	"$podman" run -d --name "$container" --hostname "$container" \
		--privileged --systemd=always --security-opt label=disable \
		--device /dev/loop-control --device "$loop" "$image" >/dev/null
	sleep 3
	"$podman" exec "$container" systemctl is-system-running >/dev/null
	"$podman" cp "$package" "$container:/tmp/remora-package"
	case "$distro" in
		ubuntu) "$podman" exec "$container" dpkg -i /tmp/remora-package ;;
		opensuse) "$podman" exec "$container" rpm -Uvh /tmp/remora-package ;;
	esac
	"$podman" exec "$container" sh -c \
		'getent group jellyfin >/dev/null || groupadd --system jellyfin; id jellyfin >/dev/null 2>&1 || useradd --system --gid jellyfin --home-dir /var/lib/jellyfin --create-home jellyfin'
	"$podman" exec "$container" mkdir -p /opt/jellyfin /usr/share/jellyfin-web
	"$podman" cp "$jellyfin_bin/." "$container:/opt/jellyfin/"
	"$podman" cp "$jellyfin_web/." "$container:/usr/share/jellyfin-web/"

	for script in setup-instance.sh; do
		"$podman" cp "$root/test/linux-systemd/$script" "$container:/root/$script"
	done
	for script in linux_real_systemd.sh linux_real_storage_fence.sh \
		linux_real_permission_fence.sh linux_real_filesystem_faults.sh \
		linux_real_process_hang.sh; do
		"$podman" cp "$root/test/$script" "$container:/root/$script"
	done

	"$podman" exec "$container" env REMORA_TEST_NAME="$distro-uat" \
		JELLYFIN_PATH=/opt/jellyfin/jellyfin /root/setup-instance.sh
	"$podman" exec "$container" /root/linux_real_systemd.sh
	"$podman" exec "$container" /root/linux_real_storage_fence.sh /srv/jellyfin-remora-uat
	"$podman" exec "$container" /root/linux_real_permission_fence.sh /srv/jellyfin-remora-uat/config
	"$podman" exec "$container" /root/linux_real_filesystem_faults.sh /srv/jellyfin-remora-uat
	"$podman" exec "$container" /root/linux_real_process_hang.sh
	"$podman" exec "$container" /usr/bin/remoractl \
		--socket /run/jellyfin-remora/remora.sock status --json > "$work/$distro-status.json"
	echo "$distro native-systemd/Jellyfin matrix passed"

	"$podman" rm -f "$container" >/dev/null
	active_containers=$(printf '%s\n' "$active_containers" | sed "s/$container //")
	losetup -d "$loop" >/dev/null 2>&1 || true
	active_loops=$(printf '%s\n' "$active_loops" | sed "s|$loop ||")
}

run_case ubuntu "$prefix-ubuntu:24.04" "$remora_deb"
run_case opensuse "$prefix-opensuse:tumbleweed" "$remora_rpm"
