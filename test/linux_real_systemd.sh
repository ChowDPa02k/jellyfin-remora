#!/bin/sh
set -eu

# Destructive native-Linux lifecycle smoke test. Run as root on a disposable
# host after Jellyfin Remora has reached RUNNING under systemd.

service=${REMORA_SERVICE:-jellyfin-remora.service}
socket=${REMORA_SOCKET:-/run/jellyfin-remora/remora.sock}
ctl=${REMORACTL:-/usr/bin/remoractl}
timeout=${REMORA_TEST_TIMEOUT:-90}

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "systemctl is required" >&2; exit 1; }
[ -x "$ctl" ] || { echo "remoractl not found: $ctl" >&2; exit 1; }

work=$(mktemp -d "${TMPDIR:-/tmp}/remora-real-systemd.XXXXXX")
cleanup() {
	if ! systemctl is-active --quiet "$service"; then
		systemctl reset-failed "$service" >/dev/null 2>&1 || true
		systemctl start "$service" >/dev/null 2>&1 || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM

status() {
	"$ctl" --socket "$socket" status --json > "$work/status.json"
}

field() {
	python3 - "$work/status.json" "$1" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    value = json.load(stream)
for component in sys.argv[2].split("."):
    value = value[component]
print(value)
PY
}

wait_running() {
	deadline=$(( $(date +%s) + timeout ))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if status 2>/dev/null && [ "$(field state)" = RUNNING ]; then
			return 0
		fi
		sleep 1
	done
	echo "timed out waiting for RUNNING" >&2
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

assert_one_exact_process() {
	status
	python3 - "$work/status.json" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    status = json.load(stream)
expected = os.path.realpath(status["executable"])
matches = []
for entry in os.listdir("/proc"):
    if not entry.isdigit():
        continue
    try:
        if os.path.realpath(os.readlink(f"/proc/{entry}/exe")) == expected:
            matches.append(int(entry))
    except (FileNotFoundError, PermissionError, OSError):
        pass
if matches != [status["pid"]]:
    raise SystemExit(f"expected only managed PID {status['pid']}, found {matches}")
if not all(item["healthy"] for item in status["storage"]):
    raise SystemExit("one or more required storage entries are unhealthy")
PY
}

wait_running
assert_one_exact_process
old_remora=$(systemctl show --property MainPID --value "$service")
old_jellyfin=$(field pid)

echo "crash test: killing Remora PID $old_remora; Jellyfin PID $old_jellyfin must survive"
systemctl kill --kill-whom=main --signal=KILL "$service"
deadline=$(( $(date +%s) + timeout ))
while [ "$(date +%s)" -lt "$deadline" ]; do
	new_remora=$(systemctl show --property MainPID --value "$service")
	if [ "$new_remora" != 0 ] && [ "$new_remora" != "$old_remora" ]; then
		break
	fi
	sleep 1
done
[ "${new_remora:-0}" != 0 ] && [ "$new_remora" != "$old_remora" ] || {
	echo "systemd did not replace Remora PID $old_remora" >&2
	exit 1
}
wait_running
[ "$(field pid)" = "$old_jellyfin" ] || {
	echo "Jellyfin PID changed after a Remora-only crash" >&2
	exit 1
}
assert_one_exact_process
echo "crash adoption passed: Remora $old_remora -> $new_remora, Jellyfin stayed $old_jellyfin"

echo "normal lifecycle test: stopping complete managed process tree"
systemctl stop "$service"
wait_pid_gone "$old_jellyfin"
systemctl start "$service"
wait_running
new_jellyfin=$(field pid)
[ "$new_jellyfin" != "$old_jellyfin" ] || {
	echo "normal service restart unexpectedly reused Jellyfin PID $old_jellyfin" >&2
	exit 1
}
assert_one_exact_process
echo "normal lifecycle passed: Jellyfin $old_jellyfin -> $new_jellyfin"
