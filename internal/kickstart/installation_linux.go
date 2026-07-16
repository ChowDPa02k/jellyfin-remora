//go:build linux

package kickstart

func platformInstallCandidates() []string {
	return []string{
		"/usr/lib/jellyfin/bin/jellyfin",
		"/usr/lib/jellyfin/jellyfin",
		"/usr/lib64/jellyfin/jellyfin",
		"/usr/bin/jellyfin",
		"/opt/jellyfin/jellyfin",
	}
}

func platformWebCandidates(string) []string {
	return []string{"/usr/share/jellyfin/web", "/usr/share/jellyfin-web"}
}
