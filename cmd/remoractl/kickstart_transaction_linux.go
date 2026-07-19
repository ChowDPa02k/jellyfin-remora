//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
)

func kickstartServiceArtifactPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), linuxServiceName)
}

func kickstartServiceExecutablePaths(string) []string {
	if os.Geteuid() != 0 {
		return nil
	}
	return []string{"/usr/local/bin/jellyfin-remora", "/usr/local/bin/remoractl"}
}

func kickstartInstalledServicePath() string {
	return filepath.Join(linuxSystemdDirectory, linuxServiceName)
}

func reloadKickstartServiceManager() error { return runLinuxSystemd("daemon-reload") }

func rollbackKickstartServiceInstallation(_ *serviceArtifact, existed bool) error {
	if existed {
		return nil
	}
	if _, err := os.Lstat(kickstartInstalledServicePath()); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := runLinuxSystemd("disable", "--now", linuxServiceName); err != nil {
		return err
	}
	return nil
}
