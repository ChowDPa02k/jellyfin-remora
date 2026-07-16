#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/remora-linux-tar-test.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
export SOURCE_DATE_EPOCH=1710000000

if "$repo/packaging/linux/package-tar.sh" '0.0.0/../../invalid' amd64 "$work/invalid" >"$work/invalid.out" 2>"$work/invalid.err"; then
	echo "unsafe version was accepted" >&2
	exit 1
fi
grep -q '^invalid version:' "$work/invalid.err"

for run in one two; do
	"$repo/packaging/linux/package-tar.sh" 0.0.0-test amd64 "$work/$run" >/dev/null
done
one="$work/one/jellyfin-remora-0.0.0-test-linux-amd64.tar.gz"
two="$work/two/jellyfin-remora-0.0.0-test-linux-amd64.tar.gz"
cmp "$one" "$two"
(cd "$work/one" && shasum -a 256 -c jellyfin-remora-0.0.0-test-linux-amd64.sha256)
contents=$(tar -tzf "$one")
for required in jellyfin-remora remoractl sample/config-linux.yaml docs/linux.md LICENSE manifest.json; do
	printf '%s\n' "$contents" | grep -q "/$required$"
done
