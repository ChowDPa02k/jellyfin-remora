#!/bin/sh
set -eu

# Destructive network-storage test for an IPv4 NFS or SMB mount. It blocks the
# protocol port, detaches the mount, requires fencing, then restores networking
# and requires Remora to mount and start exactly one replacement Jellyfin.

usage() {
	echo "usage: $0 MOUNT-TARGET SERVER-IP TCP-PORT" >&2
	exit 2
}

[ "$#" -eq 3 ] || usage
target=$1
server=$2
port=$3
service=${REMORA_SERVICE:-jellyfin-remora.service}
socket=${REMORA_SOCKET:-/run/jellyfin-remora/remora.sock}
ctl=${REMORACTL:-/usr/bin/remoractl}
timeout=${REMORA_TEST_TIMEOUT:-120}
table="remora_test_$$"

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }
command -v nft >/dev/null 2>&1 || { echo "nft is required" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 1; }
command -v mountpoint >/dev/null 2>&1 || { echo "mountpoint is required" >&2; exit 1; }
[ -x "$ctl" ] || { echo "remoractl not found: $ctl" >&2; exit 1; }
mountpoint -q "$target" || { echo "target is not mounted: $target" >&2; exit 1; }

status_file=$(mktemp "${TMPDIR:-/tmp}/remora-real-network.XXXXXX")
firewall_active=false
cleanup() {
	if [ "$firewall_active" = true ]; then
		nft delete table inet "$table" >/dev/null 2>&1 || true
	fi
	rm -f "$status_file"
	if ! systemctl is-active --quiet "$service"; then
		systemctl reset-failed "$service" >/dev/null 2>&1 || true
		systemctl start "$service" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT HUP INT TERM

status() {
	"$ctl" --socket "$socket" status --json > "$status_file"
}

field() {
	python3 - "$status_file" "$1" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    value = json.load(stream)
for component in sys.argv[2].split("."):
    value = value[component]
print(value)
PY
}

wait_state() {
	want=$1
	deadline=$(( $(date +%s) + timeout ))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if status 2>/dev/null && [ "$(field state)" = "$want" ]; then
			return 0
		fi
		sleep 1
	done
	echo "timed out waiting for $want" >&2
	return 1
}

wait_pid_gone() {
	pid=$1
	deadline=$(( $(date +%s) + timeout ))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		[ ! -e "/proc/$pid" ] && return 0
		sleep 1
	done
	echo "process $pid did not exit" >&2
	return 1
}

wait_state RUNNING
old_jellyfin=$(field pid)
echo "blocking $server:$port and detaching $target while Jellyfin PID $old_jellyfin runs"
nft add table inet "$table"
nft "add chain inet $table output { type filter hook output priority -10; policy accept; }"
nft add rule inet "$table" output ip daddr "$server" tcp dport "$port" drop
firewall_active=true
umount -l "$target"

wait_state STORAGE_FENCED
wait_pid_gone "$old_jellyfin"
status
python3 - "$status_file" "$target" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    status = json.load(stream)
target = sys.argv[2]
matches = [item for item in status["storage"] if item["target"] == target]
if len(matches) != 1 or matches[0]["healthy"] or not matches[0]["fatal"]:
    raise SystemExit(f"network target was not reported as a fatal failure: {matches}")
if status.get("pid", 0):
    raise SystemExit(f"Jellyfin still reported while fenced: PID {status['pid']}")
PY
echo "network fence passed: Jellyfin stopped while the mount remained unavailable"

nft delete table inet "$table"
firewall_active=false
wait_state RUNNING
mountpoint -q "$target" || { echo "Remora did not remount $target" >&2; exit 1; }
new_jellyfin=$(field pid)
[ "$new_jellyfin" != "$old_jellyfin" ] || {
	echo "network recovery unexpectedly reused Jellyfin PID $old_jellyfin" >&2
	exit 1
}
status
python3 - "$status_file" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    status = json.load(stream)
if not all(item["healthy"] for item in status["storage"]):
    raise SystemExit("one or more storage entries remained unhealthy after recovery")
PY
echo "network recovery passed: Jellyfin $old_jellyfin -> $new_jellyfin"
