#!/bin/sh
set -eu

# Destructive local-filesystem test. Run only on a disposable dedicated mount.
# It verifies a read-only remount and zero user-available blocks independently.

usage() {
	echo "usage: $0 PHYSICAL-MOUNT-TARGET" >&2
	exit 2
}

[ "$#" -eq 1 ] || usage
target=$1
service=${REMORA_SERVICE:-jellyfin-remora.service}
socket=${REMORA_SOCKET:-/run/jellyfin-remora/remora.sock}
ctl=${REMORACTL:-/usr/bin/remoractl}
timeout=${REMORA_TEST_TIMEOUT:-180}
fill="$target/.jellyfin-remora-full-disk-test"

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }
command -v fallocate >/dev/null 2>&1 || { echo "fallocate is required" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 1; }
command -v mountpoint >/dev/null 2>&1 || { echo "mountpoint is required" >&2; exit 1; }
[ -x "$ctl" ] || { echo "remoractl not found: $ctl" >&2; exit 1; }
mountpoint -q "$target" || { echo "target is not a mount point: $target" >&2; exit 1; }
[ ! -e "$fill" ] || { echo "refusing to replace existing file: $fill" >&2; exit 1; }

status_file=$(mktemp "${TMPDIR:-/tmp}/remora-real-filesystem.XXXXXX")
read_only=false
fill_active=false
cleanup() {
	if [ "$read_only" = true ]; then
		umount "$target" >/dev/null 2>&1 || true
	fi
	if [ "$fill_active" = true ]; then
		rm -f "$fill" >/dev/null 2>&1 || true
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

assert_fenced_target() {
	status
	python3 - "$status_file" "$target" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    status = json.load(stream)
target = sys.argv[2]
matches = [item for item in status["storage"] if item["target"] == target]
if len(matches) != 1 or matches[0]["healthy"] or not matches[0]["fatal"]:
    raise SystemExit(f"physical target was not reported as a fatal failure: {matches}")
if status.get("pid", 0):
    raise SystemExit(f"Jellyfin still reported while fenced: PID {status['pid']}")
PY
}

assert_fenced_tree() {
	status
	python3 - "$status_file" "$target" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    status = json.load(stream)
target = os.path.normpath(sys.argv[2])
matches = [
    item for item in status["storage"]
    if os.path.normpath(item["target"]) == target
    or os.path.normpath(item["target"]).startswith(target + os.sep)
]
if not any(not item["healthy"] and item["fatal"] for item in matches):
    raise SystemExit(f"no fatal target on the exhausted filesystem: {matches}")
if status.get("pid", 0):
    raise SystemExit(f"Jellyfin still reported while fenced: PID {status['pid']}")
PY
}

wait_state RUNNING
old_jellyfin=$(field pid)
echo "remounting $target read-only while Jellyfin PID $old_jellyfin runs"
mount --bind "$target" "$target"
read_only=true
mount -o remount,bind,ro "$target"
wait_state STORAGE_FENCED
wait_pid_gone "$old_jellyfin"
assert_fenced_target
echo "read-only fence passed"
umount "$target"
read_only=false
wait_state RUNNING
after_read_only=$(field pid)
[ "$after_read_only" != "$old_jellyfin" ] || {
	echo "read-only recovery unexpectedly reused Jellyfin PID $old_jellyfin" >&2
	exit 1
}
echo "read-only recovery passed: Jellyfin $old_jellyfin -> $after_read_only"

available=$(df -B1 --output=avail "$target" | tail -n 1 | tr -d ' ')
case "$available" in ''|*[!0-9]*) echo "could not determine available bytes" >&2; exit 1 ;; esac
[ "$available" -gt 0 ] || { echo "filesystem is already full" >&2; exit 1; }
echo "allocating $available bytes at $fill to exhaust user-available blocks"
fallocate -l "$available" "$fill"
fill_active=true
remaining=$(df -B1 --output=avail "$target" | tail -n 1 | tr -d ' ')
[ "$remaining" -lt 30 ] || {
	echo "filesystem still has $remaining bytes available after fill" >&2
	exit 1
}
wait_state STORAGE_FENCED
wait_pid_gone "$after_read_only"
assert_fenced_tree
echo "full-disk fence passed"
rm -f "$fill"
fill_active=false
sync
wait_state RUNNING
after_full=$(field pid)
[ "$after_full" != "$after_read_only" ] || {
	echo "full-disk recovery unexpectedly reused Jellyfin PID $after_read_only" >&2
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
echo "full-disk recovery passed: Jellyfin $after_read_only -> $after_full"
