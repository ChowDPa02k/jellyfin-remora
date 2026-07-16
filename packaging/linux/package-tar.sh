#!/bin/sh
set -eu

usage() {
	echo "usage: $0 VERSION <amd64|arm64> [OUTPUT-DIRECTORY]" >&2
	exit 2
}

[ "$#" -ge 2 ] && [ "$#" -le 3 ] || usage
version=$1
arch=$2
if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$'; then
	echo "invalid version: $version" >&2
	exit 2
fi
case "$arch" in
	amd64) directory_arch=x86_64 ;;
	arm64) directory_arch=arm64 ;;
	*) usage ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo=$(CDPATH= cd -- "$script_dir/../.." && pwd)
output=${3:-"$repo/build/linux/$directory_arch"}
epoch=${SOURCE_DATE_EPOCH:-$(date +%s)}
commit=$(git -C "$repo" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
if [ -n "$(git -C "$repo" status --porcelain --untracked-files=normal 2>/dev/null)" ]; then
	commit="$commit-dirty"
fi
build_date=$(date -u -r "$epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ)
buildinfo=github.com/ChowDPa02K/jellyfin-remora/internal/buildinfo
ldflags="-s -w -X $buildinfo.Version=$version -X $buildinfo.Commit=$commit -X $buildinfo.Date=$build_date"
base="jellyfin-remora-$version-linux-$arch"
work=$(mktemp -d "${TMPDIR:-/tmp}/jellyfin-remora-linux-package.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
stage="$work/$base"
mkdir -p "$stage/sample" "$stage/docs" "$output"

CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$stage/jellyfin-remora" "$repo/cmd/jellyfin-remora"
CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$stage/remoractl" "$repo/cmd/remoractl"
cp "$repo/sample/config-linux.yaml" "$stage/sample/"
cp "$repo/docs/linux.md" "$stage/docs/"
cp "$repo/LICENSE" "$stage/"

cat > "$stage/manifest.json" <<EOF
{
  "version": "$version",
  "commit": "$commit",
  "build_date": "$build_date",
  "target": "linux/$arch",
  "format": "portable-tarball"
}
EOF

# Normalize every inode timestamp before archiving. bsdtar on macOS does not
# support GNU tar's --mtime, so normalization happens in the staging tree.
stamp=$(date -u -r "$epoch" +%Y%m%d%H%M.%S 2>/dev/null || date -u -d "@$epoch" +%Y%m%d%H%M.%S)
find "$stage" -exec touch -h -t "$stamp" {} +
archive="$output/$base.tar.gz"
temporary="$work/$base.tar"
COPYFILE_DISABLE=1 tar --format ustar --uid 0 --gid 0 --numeric-owner -cf "$temporary" -C "$work" "$base"
gzip -n -9 < "$temporary" > "$archive"
hash=$(shasum -a 256 "$archive" 2>/dev/null | awk '{print $1}' || sha256sum "$archive" | awk '{print $1}')
printf '%s  %s\n' "$hash" "$(basename "$archive")" > "$output/$base.sha256"
printf '%s\n' "$archive"
