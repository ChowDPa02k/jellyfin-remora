//go:build windows

package main

import "github.com/ChowDPa02K/jellyfin-remora/internal/config"

// Phase 5 will generate a Windows Scheduled Task definition here.
func generatePlatformService(*config.Config, string, string) (*serviceArtifact, error) {
	return nil, nil
}
