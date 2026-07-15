//go:build linux

package main

import "github.com/ChowDPa02K/jellyfin-remora/internal/config"

func platformSampleName() (string, error) { return "config-linux.yaml", nil }
func remoraExecutableName() string        { return "jellyfin-remora" }

// Phase 5 will generate a systemd unit here. SysV init is intentionally unsupported.
func generatePlatformService(*config.Config, string, string) (*serviceArtifact, error) {
	return nil, nil
}
