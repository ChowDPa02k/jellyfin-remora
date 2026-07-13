//go:build darwin

package main

import (
	"path/filepath"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"howett.net/plist"
)

func generatePlatformService(cfg *config.Config, executable, configPath string) (*serviceArtifact, error) {
	payload := map[string]any{
		"Label":             "io.github.chowdpa02k.jellyfin-remora",
		"ProgramArguments":  []string{executable, "-c", configPath},
		"RunAtLoad":         true,
		"KeepAlive":         true,
		"ThrottleInterval":  10,
		"ProcessType":       "Background",
		"StandardOutPath":   "/var/log/jellyfin-remora.launchd.log",
		"StandardErrorPath": "/var/log/jellyfin-remora.launchd.err",
	}
	data, err := plist.MarshalIndent(payload, plist.XMLFormat, "  ")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(cfg.Jellyfin.ConfigDir, "io.github.chowdpa02k.jellyfin-remora.plist")
	if err := atomicWriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	return &serviceArtifact{Kind: "launchd plist", Path: path}, nil
}
