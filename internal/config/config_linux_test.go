//go:build linux

package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validLinuxConfig() Config {
	return Config{
		ConfigVersion: CurrentVersion,
		RESTAPI:       RESTAPIConfig{Listen: "127.0.0.1", Port: 8095},
		Remora: RemoraConfig{
			ServerStartTimeout: Duration{time.Second}, ServerStopTimeout: Duration{time.Second},
			IOTimeout: Duration{time.Second}, HeartbeatInterval: Duration{time.Second},
			Monitoring: MonitoringConfig{JellyfinAPI: JellyfinAPIMonitorConfig{Interval: Duration{time.Second}, FailureThreshold: 1}},
			Logs:       LogConfig{Level: "info"},
		},
		Jellyfin: JellyfinConfig{Path: "/bin/true", RunAsUser: "nobody", DataDir: "/srv/jf/data", ConfigDir: "/srv/jf/config", CacheDir: "/srv/jf/cache", LogDir: "/srv/jf/log"},
	}
}

func TestLinuxSampleConfigurationLoads(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "sample", "config-linux.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfigVersion != CurrentVersion || cfg.Jellyfin.RunAsUser != "jellyfin" || len(cfg.Disks) == 0 {
		t.Fatalf("unexpected Linux sample: %+v", cfg)
	}
}

func TestLinuxRejectsPlaintextSMBCredentials(t *testing.T) {
	cfg := validLinuxConfig()
	cfg.Disks = []DiskConfig{{Type: "smb", Device: "//nas/share", Target: "/mnt/share", Permission: "rw", Heartbeat: 1, FailureThreshold: 1, User: "name", Password: "secret"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "rejects SMB user/password") {
		t.Fatalf("expected plaintext rejection, got %v", err)
	}
}

func TestLinuxAcceptsFileAndLibsecretCredentials(t *testing.T) {
	for _, credential := range []string{"file:/etc/jellyfin-remora/share.credential", "/etc/jellyfin-remora/share.credential", "libsecret:media"} {
		cfg := validLinuxConfig()
		cfg.Disks = []DiskConfig{{Type: "smb", Device: "//nas/share", Target: "/mnt/share", Permission: "rw", Heartbeat: 1, FailureThreshold: 1, Credential: credential}}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("credential %q rejected: %v", credential, err)
		}
	}
}
