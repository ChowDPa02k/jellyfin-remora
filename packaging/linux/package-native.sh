#!/bin/sh
set -eu

usage() {
	echo "usage: $0 VERSION <amd64|arm64> <deb|rpm> [OUTPUT-DIRECTORY]" >&2
	exit 2
}

[ "$#" -ge 3 ] && [ "$#" -le 4 ] || usage
version=$1
arch=$2
format=$3
if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$'; then
	echo "invalid version: $version" >&2
	exit 2
fi
epoch=${SOURCE_DATE_EPOCH:-$(date +%s)}
export SOURCE_DATE_EPOCH=$epoch
stamp=$(date -u -r "$epoch" +%Y%m%d%H%M.%S 2>/dev/null || date -u -d "@$epoch" +%Y%m%d%H%M.%S)
changelog_date=$(LC_ALL=C date -u -r "$epoch" '+%a %b %d %Y' 2>/dev/null || LC_ALL=C date -u -d "@$epoch" '+%a %b %d %Y')
case "$arch" in amd64|arm64) ;; *) usage ;; esac
case "$format" in deb|rpm) ;; *) usage ;; esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo=$(CDPATH= cd -- "$script_dir/../.." && pwd)
directory_arch=$arch
[ "$arch" = amd64 ] && directory_arch=x86_64
output=${4:-"$repo/build/linux/$directory_arch"}
work=$(mktemp -d "${TMPDIR:-/tmp}/jellyfin-remora-native-package.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir -p "$output"

write_checksum() {
	package=$1
	hash=$(shasum -a 256 "$package" 2>/dev/null | awk '{print $1}' || sha256sum "$package" | awk '{print $1}')
	printf '%s  %s\n' "$hash" "$(basename "$package")" > "$package.sha256"
}

archive="$work/jellyfin-remora-$version-linux-$arch.tar.gz"
if [ -n "${REMORA_SOURCE_TARBALL:-}" ]; then
	cp "$REMORA_SOURCE_TARBALL" "$archive"
else
	"$script_dir/package-tar.sh" "$version" "$arch" "$work" >/dev/null
fi
tar -xzf "$archive" -C "$work"
source_root="$work/jellyfin-remora-$version-linux-$arch"

if [ "$format" = deb ]; then
	command -v dpkg-deb >/dev/null 2>&1 || { echo "dpkg-deb is required" >&2; exit 1; }
	deb_version=${version%%-*}
	if [ "$deb_version" != "$version" ]; then
		deb_version="$deb_version~${version#*-}"
	fi
	root="$work/deb"
	mkdir -p "$root/DEBIAN" "$root/usr/bin" "$root/usr/lib/systemd/system" \
		"$root/usr/share/doc/jellyfin-remora/examples"
	install -m 0755 "$source_root/jellyfin-remora" "$source_root/remoractl" "$root/usr/bin/"
	install -m 0644 "$script_dir/jellyfin-remora.service" "$root/usr/lib/systemd/system/"
	install -m 0644 "$source_root/sample/config-linux.yaml" "$root/usr/share/doc/jellyfin-remora/examples/"
	install -m 0644 "$source_root/docs/linux.md" "$root/usr/share/doc/jellyfin-remora/README.Linux.md"
	install -m 0644 "$source_root/LICENSE" "$root/usr/share/doc/jellyfin-remora/copyright"
	size=$(du -sk "$root/usr" | awk '{print $1}')
	cat > "$root/DEBIAN/control" <<EOF
Package: jellyfin-remora
Version: $deb_version
Section: admin
Priority: optional
Architecture: $arch
Maintainer: ChowDPa02K <noreply@github.com>
Depends: systemd
Suggests: cifs-utils, nfs-common, libsecret-tools
Installed-Size: $size
Homepage: https://github.com/ChowDPa02K/jellyfin-remora
Description: storage-aware companion supervisor for Jellyfin
 Jellyfin Remora owns a native Jellyfin process and fences it when required
 physical, SMB, or NFS storage becomes unsafe.
EOF
	cat > "$root/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  if systemctl is-active --quiet jellyfin-remora.service; then
    systemctl try-restart jellyfin-remora.service || true
  fi
fi
EOF
	cat > "$root/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = remove ] && command -v systemctl >/dev/null 2>&1; then
  systemctl disable --now jellyfin-remora.service || true
fi
EOF
	cat > "$root/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload || true; fi
if [ "$1" = purge ]; then rm -f /etc/systemd/system/jellyfin-remora.service; fi
EOF
	chmod 0755 "$root/DEBIAN/postinst" "$root/DEBIAN/prerm" "$root/DEBIAN/postrm"
	find "$root" -exec touch -h -t "$stamp" {} +
	package="$output/jellyfin-remora_${deb_version}_${arch}.deb"
	dpkg-deb --root-owner-group --uniform-compression --build "$root" "$package" >/dev/null
	write_checksum "$package"
	printf '%s\n' "$package"
	exit 0
fi

command -v rpmbuild >/dev/null 2>&1 || { echo "rpmbuild is required" >&2; exit 1; }
top="$work/rpmbuild"
mkdir -p "$top/BUILD" "$top/BUILDROOT" "$top/RPMS" "$top/SOURCES" "$top/SPECS" "$top/SRPMS"
cp "$archive" "$top/SOURCES/"
cp "$script_dir/jellyfin-remora.service" "$top/SOURCES/"
rpm_version=${version%%-*}
if [ "$rpm_version" = "$version" ]; then
	rpm_release=1
else
	rpm_release="0.$(printf '%s' "${version#*-}" | tr -c 'A-Za-z0-9.' '.')"
fi
rpm_arch=x86_64
[ "$arch" = arm64 ] && rpm_arch=aarch64
cat > "$top/SPECS/jellyfin-remora.spec" <<EOF
Name: jellyfin-remora
Version: $rpm_version
Release: $rpm_release%{?dist}
Summary: Storage-aware companion supervisor for Jellyfin
License: MIT
URL: https://github.com/ChowDPa02K/jellyfin-remora
Source0: $(basename "$archive")
Source1: jellyfin-remora.service
Requires: systemd
%global debug_package %{nil}
%global __brp_strip %{nil}
%global __brp_strip_comment_note %{nil}
%global __brp_strip_static_archive %{nil}

%description
Jellyfin Remora owns a native Jellyfin process and fences it when required
physical, SMB, or NFS storage becomes unsafe.

%prep
%setup -q -n jellyfin-remora-$version-linux-$arch

%install
install -D -m 0755 jellyfin-remora %{buildroot}%{_bindir}/jellyfin-remora
install -D -m 0755 remoractl %{buildroot}%{_bindir}/remoractl
install -D -m 0644 %{SOURCE1} %{buildroot}%{_unitdir}/jellyfin-remora.service
install -D -m 0644 sample/config-linux.yaml %{buildroot}%{_docdir}/jellyfin-remora/examples/config-linux.yaml
install -D -m 0644 docs/linux.md %{buildroot}%{_docdir}/jellyfin-remora/README.Linux.md
install -D -m 0644 LICENSE %{buildroot}%{_licensedir}/jellyfin-remora/LICENSE

%post
systemctl daemon-reload >/dev/null 2>&1 || :
if systemctl is-active --quiet jellyfin-remora.service; then systemctl try-restart jellyfin-remora.service || :; fi

%preun
if [ \$1 -eq 0 ]; then systemctl disable --now jellyfin-remora.service >/dev/null 2>&1 || :; fi

%postun
systemctl daemon-reload >/dev/null 2>&1 || :
if [ \$1 -eq 0 ]; then rm -f /etc/systemd/system/jellyfin-remora.service; fi

%files
%{_bindir}/jellyfin-remora
%{_bindir}/remoractl
%{_unitdir}/jellyfin-remora.service
%doc %{_docdir}/jellyfin-remora
%license %{_licensedir}/jellyfin-remora/LICENSE

%changelog
* $changelog_date ChowDPa02K <noreply@github.com> - $rpm_version-$rpm_release
- Native Linux alpha package.
EOF
rpm_log="$work/rpmbuild.log"
if ! rpmbuild --target "${rpm_arch}-linux" \
	--define "_topdir $top" \
	--define "_buildhost jellyfin-remora-builder" \
	--define "_unitdir /usr/lib/systemd/system" \
	--define "_licensedir /usr/share/licenses" \
	--define "clamp_mtime_to_source_date_epoch 1" \
	--define "use_source_date_epoch_as_buildtime 1" \
	-bb "$top/SPECS/jellyfin-remora.spec" >"$rpm_log" 2>&1; then
	cat "$rpm_log" >&2
	exit 1
fi
package=$(find "$top/RPMS" -type f -name '*.rpm' -print | head -1)
destination="$output/$(basename "$package")"
cp "$package" "$destination"
write_checksum "$destination"
printf '%s\n' "$destination"
