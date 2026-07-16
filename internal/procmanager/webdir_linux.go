//go:build linux

package procmanager

import (
	"os"
	"path/filepath"
)

func platformDefaultWebDir(executable string) (string, error) {
	candidates := []string{
		filepath.Join(filepath.Dir(executable), "jellyfin-web"),
		filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "jellyfin-web")),
		"/usr/share/jellyfin-web",
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	// Server-only packages can intentionally run without bundled Web assets.
	// Jellyfin decides whether that is acceptable; Remora does not invent a
	// path that would make an otherwise valid API-only deployment fail early.
	return "", nil
}
