#!/bin/sh
set -eu

# Destructive run-as-user probe test. Removes write permission from a managed
# Jellyfin directory, requires fencing, restores the mode, then requires one
# controlled replacement process.

usage() {
	echo "usage: $0 WRITABLE-JELLYFIN-DIRECTORY" >&2
	exit 2
}

[ "$#" -eq 1 ] || usage
target=$1
service=${REMORA_SERVICE:-jellyfin-remora.service}
socket=${REMORA_SOCKET:-/run/jellyfin-remora/remora.sock}
ctl=${REMORACTL:-/usr/bin/remoractl}
timeout=${REMORA_TEST_TIMEOUT:-90}

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 1; }
[ -d "$target" ] || { echo "directory not found: $target" >&2; exit 1; }
[ -x "$ctl" ] || { echo "remoractl not found: $ctl" >&2; exit 1; }

original_mode=$(stat -c %a "$target")
status_file=$(mktemp "${TMPDIR:-/tmp}/remora-real-permission.XXXXXX")
mode_changed=false
cleanup() {
	if [ "$mode_changed" = true ]; then
		chmod "$original_mode" "$target" >/dev/null 2>&1 || true
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
echo "removing write permission from $target while Jellyfin PID $old_jellyfin runs"
chmod a-w "$target"
mode_changed=true
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
    raise SystemExit(f"path did not report a fatal permission failure: {matches}")
if status.get("pid", 0):
    raise SystemExit(f"Jellyfin still reported while fenced: PID {status['pid']}")
PY
echo "permission fence passed: the run-as-user probe stopped Jellyfin"

chmod "$original_mode" "$target"
mode_changed=false
wait_state RUNNING
new_jellyfin=$(field pid)
[ "$new_jellyfin" != "$old_jellyfin" ] || {
	echo "permission recovery unexpectedly reused Jellyfin PID $old_jellyfin" >&2
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
echo "permission recovery passed: Jellyfin $old_jellyfin -> $new_jellyfin"
