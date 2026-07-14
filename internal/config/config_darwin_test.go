//go:build darwin

package config

import (
	"path/filepath"
	"testing"
)

func TestDarwinSampleLoadsAsCurrentConfiguration(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "sample", "config-darwin.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfigVersion != CurrentVersion {
		t.Fatalf("config version = %d, want %d", cfg.ConfigVersion, CurrentVersion)
	}
	if len(cfg.Disks) != 1 || cfg.Disks[0].FailureThreshold != 1 {
		t.Fatalf("sample disks = %+v", cfg.Disks)
	}
}
