//go:build !darwin && !linux && !windows

package main

func kickstartServiceArtifactPath(string) string                        { return "" }
func kickstartServiceExecutablePaths(string) []string                   { return nil }
func kickstartInstalledServicePath() string                             { return "" }
func reloadKickstartServiceManager() error                              { return nil }
func rollbackKickstartServiceInstallation(*serviceArtifact, bool) error { return nil }
