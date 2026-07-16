//go:build windows

package kickstart

import (
	"os"
	"path/filepath"
)

func platformInstallCandidates() []string {
	var result []string
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramW6432")} {
		if root != "" {
			result = append(result, filepath.Join(root, "Jellyfin", "Server", "jellyfin.exe"))
		}
	}
	return result
}

func platformWebCandidates(executable string) []string {
	return []string{filepath.Join(filepath.Dir(executable), "jellyfin-web"), filepath.Join(filepath.Dir(executable), "wwwroot")}
}
