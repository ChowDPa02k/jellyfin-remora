//go:build darwin

package procmanager

import (
	"fmt"
	"os"
	"path/filepath"
)

func platformDefaultWebDir(executable string) (string, error) {
	macOSDir := filepath.Dir(executable)
	if filepath.Base(macOSDir) != "MacOS" || filepath.Base(filepath.Dir(macOSDir)) != "Contents" {
		return "", nil
	}
	candidate := filepath.Clean(filepath.Join(macOSDir, "..", "Resources", "jellyfin-web"))
	if st, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !st.IsDir() {
		return candidate, nil
	}
	return "", fmt.Errorf("Jellyfin web resources not found under app bundle: %s", candidate)
}
