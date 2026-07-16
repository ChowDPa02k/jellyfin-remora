#!/bin/sh
set -eu

# Destructive health/restart test. SIGSTOP makes Jellyfin unable to answer its
# API or handle graceful termination; Remora must escalate, reap the old tree,
# and start exactly one replacement.

service=${REMORA_SERVICE:-jellyfin-remora.service}
socket=${REMORA_SOCKET:-/run/jellyfin-remora/remora.sock}
ctl=${REMORACTL:-/usr/bin/remoractl}
timeout=${REMORA_TEST_TIMEOUT:-120}

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 1; }
[ -x "$ctl" ] || { echo "remoractl not found: $ctl" >&2; exit 1; }

status_file=$(mktemp "${TMPDIR:-/tmp}/remora-real-hang.XXXXXX")
stopped_pid=
cleanup() {
	if [ -n "$stopped_pid" ] && [ -e "/proc/$stopped_pid" ]; then
		kill -CONT "$stopped_pid" >/dev/null 2>&1 || true
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

deadline=$(( $(date +%s) + timeout ))
while [ "$(date +%s)" -lt "$deadline" ]; do
	if status 2>/dev/null && [ "$(field state)" = RUNNING ]; then
		break
	fi
	sleep 1
done
[ "$(field state)" = RUNNING ] || { echo "initial state is not RUNNING" >&2; exit 1; }
old_jellyfin=$(field pid)
stopped_pid=$old_jellyfin
echo "stopping all scheduling for Jellyfin PID $old_jellyfin"
kill -STOP "$old_jellyfin"

replacement=
deadline=$(( $(date +%s) + timeout ))
while [ "$(date +%s)" -lt "$deadline" ]; do
	if status 2>/dev/null; then
		state=$(field state)
		pid=$(field pid 2>/dev/null || true)
		if [ "$state" = RUNNING ] && [ -n "$pid" ] && [ "$pid" != "$old_jellyfin" ]; then
			replacement=$pid
			break
		fi
	fi
	sleep 1
done
[ -n "$replacement" ] || { echo "timed out waiting for replacement Jellyfin" >&2; exit 1; }
[ ! -e "/proc/$old_jellyfin" ] || { echo "stopped Jellyfin PID $old_jellyfin survived restart" >&2; exit 1; }
stopped_pid=
python3 - "$status_file" <<'PY'
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
PY
echo "hung-process recovery passed: Jellyfin $old_jellyfin -> $replacement"
