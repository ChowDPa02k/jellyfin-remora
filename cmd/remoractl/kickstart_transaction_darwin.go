//go:build darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
)

func kickstartServiceArtifactPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), darwinServiceLabel+".plist")
}

func kickstartServiceExecutablePaths(string) []string { return nil }
func kickstartInstalledServicePath() string           { return darwinInstalledServicePath() }
func reloadKickstartServiceManager() error            { return nil }

func rollbackKickstartServiceInstallation(_ *serviceArtifact, existed bool) error {
	if existed {
		return nil
	}
	if _, err := os.Lstat(darwinInstalledServicePath()); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := runDarwinLaunchctl("print", "system/"+darwinServiceLabel); err != nil {
		return nil
	}
	return runDarwinLaunchctl("bootout", "system/"+darwinServiceLabel)
}
