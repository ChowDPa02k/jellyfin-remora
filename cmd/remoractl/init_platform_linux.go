//go:build linux

package main

import "github.com/ChowDPa02K/jellyfin-remora/internal/config"

// Phase 4 will generate a systemd unit here. SysV init is intentionally unsupported.
func generatePlatformService(*config.Config, string, string) (*serviceArtifact, error) {
	return nil, nil
}
