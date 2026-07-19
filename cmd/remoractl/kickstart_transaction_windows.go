//go:build windows

package main

import "path/filepath"

func kickstartServiceArtifactPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "install-jellyfin-remora.ps1")
}

func kickstartServiceExecutablePaths(string) []string { return nil }
func kickstartInstalledServicePath() string           { return "" }
func reloadKickstartServiceManager() error            { return nil }

func rollbackKickstartServiceInstallation(artifact *serviceArtifact, existed bool) error {
	if existed {
		return nil
	}
	return runPowerShellInstaller(artifact.Path, "UninstallTask")
}
