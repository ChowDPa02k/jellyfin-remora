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
	if cfg.RESTAPI.UnixSocket != "/tmp/.s.remora.8095" {
		t.Fatalf("Unix socket = %q", cfg.RESTAPI.UnixSocket)
	}
	if len(cfg.Disks) != 1 || cfg.Disks[0].FailureThreshold != 1 {
		t.Fatalf("sample disks = %+v", cfg.Disks)
	}
}

func TestDarwinRejectsWindowsOnlyConfiguration(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "named pipe", mutate: func(cfg *Config) { cfg.RESTAPI.NamedPipe = `\\.\pipe\remora` }},
		{name: "volume GUID", mutate: func(cfg *Config) {
			cfg.Disks[0].UUID = ""
			cfg.Disks[0].VolumeGUID = `\\?\Volume{00000000-0000-0000-0000-000000000000}\`
		}},
		{name: "volume label", mutate: func(cfg *Config) { cfg.Disks[0].VolumeLabel = "STORAGE" }},
		{name: "filesystem", mutate: func(cfg *Config) { cfg.Disks[0].Filesystem = "NTFS" }},
		{name: "credential manager", mutate: func(cfg *Config) {
			cfg.Disks[0].Type = "smb"
			cfg.Disks[0].UUID = ""
			cfg.Disks[0].Device = "//server/share"
			cfg.Disks[0].Credential = "windows-credential-manager"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := Load(filepath.Join("..", "..", "sample", "config-darwin.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Windows-only configuration was accepted on Darwin")
			}
		})
	}
}
