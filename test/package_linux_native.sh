#!/bin/sh
set -eu

usage() {
	echo "usage: $0 <amd64|arm64> <deb|rpm>" >&2
	exit 2
}

[ "$#" -eq 2 ] || usage
arch=$1
format=$2
case "$arch" in amd64|arm64) ;; *) usage ;; esac
case "$format" in deb|rpm) ;; *) usage ;; esac

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/remora-linux-native-test.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
export SOURCE_DATE_EPOCH=1710000000
version=0.0.0-test

if "$repo/packaging/linux/package-native.sh" '0.0.0/../../invalid' "$arch" "$format" "$work/invalid" >"$work/invalid.out" 2>"$work/invalid.err"; then
	echo "unsafe version was accepted" >&2
	exit 1
fi
grep -q '^invalid version:' "$work/invalid.err"

"$repo/packaging/linux/package-tar.sh" "$version" "$arch" "$work/input" >/dev/null
export REMORA_SOURCE_TARBALL="$work/input/jellyfin-remora-$version-linux-$arch.tar.gz"
for run in one two; do
	"$repo/packaging/linux/package-native.sh" "$version" "$arch" "$format" "$work/$run" >/dev/null
done
one=$(find "$work/one" -type f -name "*.$format" -print | head -1)
two=$(find "$work/two" -type f -name "*.$format" -print | head -1)
[ -n "$one" ] && [ -n "$two" ]
cmp "$one" "$two"
test -f "$one.sha256" && test -f "$two.sha256"
cmp "$one.sha256" "$two.sha256"
if command -v sha256sum >/dev/null 2>&1; then
	(cd "$(dirname "$one")" && sha256sum -c "$(basename "$one").sha256")
else
	(cd "$(dirname "$one")" && shasum -a 256 -c "$(basename "$one").sha256")
fi

if [ "$format" = deb ]; then
	[ "$(dpkg-deb -f "$one" Architecture)" = "$arch" ]
	[ "$(dpkg-deb -f "$one" Version)" = "0.0.0~test" ]
	dpkg --compare-versions "0.0.0~test" lt "0.0.0"
else
	want=$arch
	[ "$arch" = arm64 ] && want=aarch64
	[ "$(rpm -qp --qf '%{ARCH}' "$one")" = "$want" ]
fi
printf '%s\n' "$one"
