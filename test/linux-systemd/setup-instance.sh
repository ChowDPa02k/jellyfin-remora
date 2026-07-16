#!/bin/sh
set -eu

# Prepare a disposable native-systemd Jellyfin Remora instance inside a
# privileged test container. The caller installs Remora and Jellyfin first.

name=${REMORA_TEST_NAME:-container-uat}
image=${REMORA_TEST_DISK_IMAGE:-/var/lib/jellyfin-remora-uat.ext4}
target=${REMORA_TEST_STORAGE:-/srv/jellyfin-remora-uat}
jellyfin=${JELLYFIN_PATH:-/usr/lib/jellyfin/bin/jellyfin}
web=${JELLYFIN_WEB_DIR:-/usr/share/jellyfin-web}
config=${REMORA_CONFIG:-/etc/jellyfin-remora/remora-config.yaml}
# Jellyfin 10.11 refuses to start below 2 GiB free, so leave filesystem
# metadata and test-headroom above that product threshold.
size=${REMORA_TEST_DISK_SIZE:-4G}

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "systemctl is required" >&2; exit 1; }
command -v mkfs.ext4 >/dev/null 2>&1 || { echo "mkfs.ext4 is required" >&2; exit 1; }
command -v findmnt >/dev/null 2>&1 || { echo "findmnt is required" >&2; exit 1; }
[ -x "$jellyfin" ] || { echo "Jellyfin executable not found: $jellyfin" >&2; exit 1; }
[ -d "$web" ] || { echo "Jellyfin Web directory not found: $web" >&2; exit 1; }
id jellyfin >/dev/null 2>&1 || { echo "jellyfin user is required" >&2; exit 1; }

systemctl disable --now jellyfin-remora.service >/dev/null 2>&1 || true
mkdir -p "$target" "$(dirname "$config")"
if ! mountpoint -q "$target"; then
	if [ ! -f "$image" ]; then
		truncate -s "$size" "$image"
		mkfs.ext4 -q -F -L REMORA_UAT "$image"
	fi
	mount -o loop "$image" "$target"
fi

device=$(findmnt --noheadings --output SOURCE --target "$target")
uuid=$(blkid -s UUID -o value "$device")
[ -n "$uuid" ] || { echo "could not resolve UUID for $device" >&2; exit 1; }
# Minimal systemd containers do not run udev, so synthesize the stable identity
# link that is present on normal Linux hosts.
if [ ! -e "/dev/disk/by-uuid/$uuid" ]; then
	mkdir -p /dev/disk/by-uuid
	ln -s "$(realpath --relative-to=/dev/disk/by-uuid "$device")" "/dev/disk/by-uuid/$uuid"
fi

install -d -o jellyfin -g jellyfin -m 0750 \
	"$target/data" "$target/config" "$target/cache" "$target/log"
chown jellyfin:jellyfin "$target"
chmod 0750 "$target"

cat > "$config" <<EOF
config-version: 2
restapi:
  listen: 127.0.0.1
  port: 8095
  unix-socket: /run/jellyfin-remora/remora.sock
remora:
  server-start-timeout: 180s
  server-stop-timeout: 5s
  io-timeout: 3s
  recovery-successes: 2
  monitoring:
    interval: 1s
    jellyfin-api:
      interval: 2s
      failure-threshold: 3
    user-login:
      enabled: false
      interval: 60s
      user: remora
      password: remora-test-only
  data-dir: /var/lib/jellyfin-remora
  logs:
    path: /var/log/jellyfin-remora
    level: info
    rotation-time: 24h
    rotation-size-mb: 10
    preserve-time: 1d
disk:
  - type: physical
    uuid: $uuid
    target: $target
    permission: rw
    heartbeat: 1
    failure-threshold: 1
jellyfin:
  path: $jellyfin
  run-as-user: jellyfin
  run-as-group: jellyfin
  data-dir: $target/data
  config-dir: $target/config
  cache-dir: $target/cache
  log-dir: $target/log
  web-dir: $web
  general:
    settings:
      server-name: $name
  networking:
    server-address-settings:
      local-http-port-number: 8096
      local-https-port-number: 8920
      enable-https: false
      base-url: null
      bind-to-local-network-address: null
    ip-protocols:
      enable-ipv4: true
      enable-ipv6: false
init:
  server-name: $name
  display-language: English
  user: admin
  password: remora-test-only
  preferred-metadata-language: English
  preferred-metadata-region: United States
  allow-remote-connections: true
EOF
chmod 0600 "$config"

systemctl daemon-reload
systemctl enable --now jellyfin-remora.service

deadline=$(( $(date +%s) + 180 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
	if /usr/bin/remoractl --socket /run/jellyfin-remora/remora.sock status --json \
		>/tmp/remora-container-status.json 2>/dev/null; then
		state=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["state"])' \
			/tmp/remora-container-status.json 2>/dev/null || true)
		if [ "$state" = RUNNING ]; then
			cat /tmp/remora-container-status.json
			exit 0
		fi
	fi
	sleep 1
done

systemctl status jellyfin-remora.service --no-pager >&2 || true
journalctl -u jellyfin-remora.service --no-pager -n 100 >&2 || true
exit 1
