//go:build darwin

package kickstart

import "path/filepath"

func platformInstallCandidates() []string {
	root := "/Applications/Jellyfin.app/Contents/MacOS"
	return []string{filepath.Join(root, "jellyfin"), filepath.Join(root, "Jellyfin"), filepath.Join(root, "Jellyfin Server")}
}

func platformWebCandidates(string) []string {
	return []string{"/Applications/Jellyfin.app/Contents/Resources/jellyfin-web"}
}
