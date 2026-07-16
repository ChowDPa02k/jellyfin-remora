#!/bin/sh
set -eu

# Destructive, self-restoring database corruption test for a disposable
# Jellyfin data directory on NFS. Remora itself never opens the database; this
# harness emulates an external client damaging live SQLite pages.

service=${REMORA_SERVICE:-jellyfin-remora.service}
socket=${REMORA_SOCKET:-/run/jellyfin-remora/remora.sock}
ctl=${REMORACTL:-/usr/bin/remoractl}
database=${REMORA_TEST_DATABASE:-}
timeout=${REMORA_TEST_TIMEOUT:-180}

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }
[ "${REMORA_DATABASE_DAMAGE_CONFIRM:-}" = "YES-DESTROY-DISPOSABLE-DATABASE" ] || {
	echo "set REMORA_DATABASE_DAMAGE_CONFIRM=YES-DESTROY-DISPOSABLE-DATABASE" >&2
	exit 1
}
[ -n "$database" ] && [ -f "$database" ] || {
	echo "REMORA_TEST_DATABASE must name the disposable live jellyfin.db" >&2
	exit 1
}
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 1; }
command -v findmnt >/dev/null 2>&1 || { echo "findmnt is required" >&2; exit 1; }
command -v sqlite3 >/dev/null 2>&1 || { echo "sqlite3 is required for the stopped recovery snapshot" >&2; exit 1; }
[ -x "$ctl" ] || { echo "remoractl not found: $ctl" >&2; exit 1; }

filesystem=$(findmnt -n -o FSTYPE -T "$database")
case "$filesystem" in
	nfs|nfs4) ;;
	*) echo "database is not on NFS: $database ($filesystem)" >&2; exit 1 ;;
esac

work=$(mktemp -d "${TMPDIR:-/var/tmp}/remora-real-db-damage.XXXXXX")
status_file=$work/status.json
backup=$work/jellyfin.db
database_modified=false

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

remove_sidecars() {
	for suffix in -wal -shm; do
		[ ! -e "$database$suffix" ] || rm -f "$database$suffix"
	done
}

restore_database() {
	"$ctl" --socket "$socket" stop >/dev/null 2>&1 || true
	remove_sidecars
	cp -p "$backup" "$database"
	sync "$database" 2>/dev/null || sync
	database_modified=false
}

cleanup() {
	if [ "$database_modified" = true ] && [ -f "$backup" ]; then
		echo "restoring disposable database after interrupted test" >&2
		restore_database
		"$ctl" --socket "$socket" start >/dev/null 2>&1 || true
	fi
	rm -rf "$work"
}

signal_cleanup() {
	trap - EXIT HUP INT TERM
	cleanup
	exit 130
}

trap cleanup EXIT
trap signal_cleanup HUP INT TERM

wait_state RUNNING
status
old_pid=$(field pid)
case "$(python3 - "$status_file" "$database" <<'PY'
import json
import os
import sys

status = json.load(open(sys.argv[1], encoding="utf-8"))
database = os.path.realpath(sys.argv[2])
data_dirs = [arg.split("=", 1)[1] for arg in status.get("arguments", []) if arg.startswith("--datadir=")]
print("yes" if data_dirs and os.path.commonpath([database, os.path.realpath(data_dirs[0])]) == os.path.realpath(data_dirs[0]) else "no")
PY
)" in
	yes) ;;
	*) echo "database is not beneath the managed Jellyfin --datadir" >&2; exit 1 ;;
esac

# Obtain a coherent recovery copy only while Jellyfin is stopped.
"$ctl" --socket "$socket" stop >/dev/null
wait_state STOPPED
sqlite3 "$database" 'PRAGMA wal_checkpoint(TRUNCATE);' >/dev/null
remove_sidecars
cp -p "$database" "$backup"

# A newly initialized Jellyfin database can fit completely in SQLite's page
# cache, so overwriting it from the same NFS client may not force another page
# read. Add disposable, valid activity rows while Jellyfin is stopped. The
# pristine snapshot above is restored after the test, so these rows never
# survive the harness.
if [ "$(stat -c %s "$database")" -lt $((32 * 1024 * 1024)) ]; then
	sqlite3 "$database" <<'SQL'
BEGIN;
WITH RECURSIVE n(x) AS (
  VALUES(1)
  UNION ALL
  SELECT x + 1 FROM n WHERE x < 30000
), base(v) AS (
  SELECT COALESCE(MAX(RowVersion), 0) FROM ActivityLogs
)
INSERT INTO ActivityLogs(
  Name, Overview, ShortOverview, Type, UserId, ItemId,
  DateCreated, LogSeverity, RowVersion
)
SELECT
  printf('Remora database-damage pressure row %d', x),
  printf('%01024d', x),
  NULL,
  'RemoraDatabaseDamageTest',
  '00000000-0000-0000-0000-000000000000',
  NULL,
  strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
  2,
  base.v + x
FROM n, base;
COMMIT;
SQL
	database_modified=true
	sqlite3 "$database" 'PRAGMA integrity_check;' | grep -qx ok || {
		echo "pressure preparation made the disposable database invalid" >&2
		exit 1
	}
	echo "prepared a disposable $(stat -c %s "$database")-byte database to exceed SQLite page cache"
fi
"$ctl" --socket "$socket" start >/dev/null
wait_state RUNNING
status
test_pid=$(field pid)

size=$(stat -c %s "$database")
[ "$size" -ge 4096 ] || { echo "database is unexpectedly small: $size" >&2; exit 1; }
blocks=$(( (size + 4095) / 4096 ))
echo "destroying $blocks live SQLite pages on NFS for managed PID $test_pid"
dd if=/dev/zero of="$database" bs=4096 count="$blocks" conv=notrunc status=none
sync "$database" 2>/dev/null || sync
database_modified=true

wait_state DATABASE_DAMAGED
status
[ "$(field database.damaged)" = True ] || { echo "database damage flag was not latched" >&2; exit 1; }
[ ! -e "/proc/$test_pid" ] || { echo "damaged Jellyfin PID $test_pid is still running" >&2; exit 1; }

set +e
"$ctl" --socket "$socket" restart >/dev/null 2>&1
restart_code=$?
set -e
[ "$restart_code" -eq 4 ] || { echo "restart exit=$restart_code, want 4" >&2; exit 1; }

systemctl restart "$service"
wait_state DATABASE_DAMAGED
[ ! -e "/proc/$test_pid" ] || { echo "daemon restart revived damaged Jellyfin" >&2; exit 1; }

restore_database
"$ctl" --socket "$socket" start >/dev/null
wait_state RUNNING
status
recovered_pid=$(field pid)
[ "$recovered_pid" != "$test_pid" ] || { echo "recovery reused the damaged process" >&2; exit 1; }
[ "$(field database.damaged)" = False ] || { echo "database fence remained set after repaired start" >&2; exit 1; }
echo "database-damage fence passed on $filesystem: initial=$old_pid test=$test_pid recovered=$recovered_pid"
