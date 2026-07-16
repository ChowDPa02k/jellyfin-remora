#!/bin/sh
set -eu

# Native arm64 release gate. This script intentionally modifies systemd,
# creates a loop-backed ext4 filesystem, and downloads pinned official
# Jellyfin packages. Run only as root on a disposable Ubuntu 24.04 arm64 host.

case "$(uname -m)" in
	aarch64|arm64) ;;
	*) echo "native arm64 kernel required; refusing emulation on $(uname -m)" >&2; exit 1 ;;
esac
[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/remora-real-arm64.XXXXXX")
storage=/srv/jellyfin-remora-arm64-uat
image=/var/lib/jellyfin-remora-arm64-uat.ext4
server_url='https://repo.jellyfin.org/files/debian/pool/main/j/jellyfin/jellyfin-server_10.11.11%2Bdeb13_arm64.deb'
web_url='https://repo.jellyfin.org/files/debian/pool/main/j/jellyfin/jellyfin-web_10.11.11%2Bdeb13_all.deb'
server_sha=ce4354be8885682ff12be9ee338c3b0d8558ee9d7930ded4864c90528cc396ee
web_sha=92faa02728f30f95799e1b9f0c4789b2eaef28bbe0daee13b377b5db97a5e888

cleanup() {
	systemctl disable --now jellyfin-remora.service >/dev/null 2>&1 || true
	if mountpoint -q "$storage"; then umount "$storage" >/dev/null 2>&1 || true; fi
	dpkg -r jellyfin-remora >/dev/null 2>&1 || true
	rm -rf "$storage" "$image" /opt/jellyfin-remora-arm64 \
		/etc/jellyfin-remora /var/lib/jellyfin-remora /var/log/jellyfin-remora "$work"
}
trap cleanup EXIT HUP INT TERM

# GitHub checks out as the runner account, while this destructive gate runs as
# root so it can own systemd and mount operations.
git config --global --add safe.directory "$root"

export DEBIAN_FRONTEND=noninteractive
apt_get() {
	attempt=1
	while ! apt-get \
		-o Acquire::ForceIPv4=true \
		-o Acquire::Retries=5 \
		-o Acquire::http::Timeout=30 \
		-o Acquire::https::Timeout=30 \
		"$@"; do
		[ "$attempt" -lt 3 ] || return 1
		echo "apt-get $* failed (attempt $attempt/3); retrying" >&2
		sleep $((attempt * 5))
		attempt=$((attempt + 1))
	done
}

apt_get update
# The server package recommends jellyfin-ffmpeg7 or ffmpeg. Keep a supported
# runtime even though this lifecycle/storage gate does not perform a transcode.
apt_get install --yes --no-install-recommends \
	ca-certificates curl e2fsprogs ffmpeg file libfontconfig1 libjemalloc2 python3

curl --fail --location --retry 3 --output "$work/jellyfin-server.deb" "$server_url"
curl --fail --location --retry 3 --output "$work/jellyfin-web.deb" "$web_url"
printf '%s  %s\n' "$server_sha" "$work/jellyfin-server.deb" | sha256sum --check
printf '%s  %s\n' "$web_sha" "$work/jellyfin-web.deb" | sha256sum --check

mkdir -p "$work/server" "$work/web" "$work/packages"
dpkg-deb -x "$work/jellyfin-server.deb" "$work/server"
dpkg-deb -x "$work/jellyfin-web.deb" "$work/web"
test -x "$work/server/usr/lib/jellyfin/bin/jellyfin"
test -d "$work/web/usr/share/jellyfin/web"
file -b "$work/server/usr/lib/jellyfin/bin/jellyfin" | \
	grep -Eq 'ARM aarch64|ARM64' || { echo "Jellyfin package is not arm64" >&2; exit 1; }

version=$(awk '/^VERSION[[:space:]]*\?=/ { print $3; exit }' "$root/Makefile")
[ -n "$version" ] || { echo "could not resolve project version" >&2; exit 1; }
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git -C "$root" show -s --format=%ct HEAD)} \
	"$root/packaging/linux/package-native.sh" "$version" arm64 deb "$work/packages"
package=$(find "$work/packages" -maxdepth 1 -name 'jellyfin-remora_*_arm64.deb' -print | head -1)
[ -n "$package" ] || { echo "arm64 Remora DEB was not produced" >&2; exit 1; }
dpkg -i "$package"

getent group jellyfin >/dev/null || groupadd --system jellyfin
id jellyfin >/dev/null 2>&1 || \
	useradd --system --gid jellyfin --home-dir /var/lib/jellyfin --create-home jellyfin
install -d -m 0755 /opt/jellyfin-remora-arm64/server /opt/jellyfin-remora-arm64/web
cp -a "$work/server/usr/lib/jellyfin/bin/." /opt/jellyfin-remora-arm64/server/
cp -a "$work/web/usr/share/jellyfin/web/." /opt/jellyfin-remora-arm64/web/

REMORA_TEST_NAME=ubuntu-arm64-uat \
	REMORA_TEST_STORAGE="$storage" \
	REMORA_TEST_DISK_IMAGE="$image" \
	JELLYFIN_PATH=/opt/jellyfin-remora-arm64/server/jellyfin \
	JELLYFIN_WEB_DIR=/opt/jellyfin-remora-arm64/web \
	"$root/test/linux-systemd/setup-instance.sh"

"$root/test/linux_real_systemd.sh"
"$root/test/linux_real_storage_fence.sh" "$storage"
"$root/test/linux_real_permission_fence.sh" "$storage/config"
"$root/test/linux_real_filesystem_faults.sh" "$storage"
"$root/test/linux_real_process_hang.sh"

/usr/bin/remoractl --socket /run/jellyfin-remora/remora.sock status --json \
	> "$work/final-status.json"
python3 - "$work/final-status.json" <<'PY'
import json
import platform
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    status = json.load(stream)
if platform.machine() not in {"aarch64", "arm64"}:
    raise SystemExit("test did not run on a native arm64 kernel")
if status["state"] != "RUNNING" or status["version"] != "10.11.11":
    raise SystemExit(f"unexpected final status: {status}")
if not all(item["healthy"] for item in status["storage"]):
    raise SystemExit("storage did not recover completely")
print(f"native arm64 Jellyfin gate passed with PID {status['pid']}")
PY
