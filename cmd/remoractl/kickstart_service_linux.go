//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func prepareKickstartServiceExecutable(remoraExecutable string) (string, error) {
	if os.Geteuid() != 0 {
		return remoraExecutable, nil
	}
	destinationDir := "/usr/local/bin"
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		return "", err
	}
	for _, source := range []string{remoraExecutable, filepath.Join(filepath.Dir(remoraExecutable), "remoractl")} {
		data, err := os.ReadFile(source)
		if err != nil {
			return "", fmt.Errorf("read kickstart binary %s: %w", source, err)
		}
		destination := filepath.Join(destinationDir, filepath.Base(source))
		if err := atomicWriteFile(destination, data, 0o755); err != nil {
			return "", fmt.Errorf("install kickstart binary %s: %w", destination, err)
		}
		if err := os.Chown(destination, 0, 0); err != nil {
			return "", err
		}
	}
	fmt.Printf("Remora binaries installed: %s\n", destinationDir)
	return filepath.Join(destinationDir, "jellyfin-remora"), nil
}
