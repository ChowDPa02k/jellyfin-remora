//go:build windows

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsSampleLoadsAsCurrentConfiguration(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "sample", "config-windows.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfigVersion != CurrentVersion {
		t.Fatalf("config version = %d, want %d", cfg.ConfigVersion, CurrentVersion)
	}
	if cfg.RESTAPI.TCPControlEnabled() {
		t.Fatal("Windows sample unexpectedly enables loopback TCP control")
	}
	if len(cfg.Disks) != 1 || cfg.Disks[0].VolumeGUID == "" || cfg.Disks[0].ProbePath == "" {
		t.Fatalf("sample physical disk = %+v", cfg.Disks)
	}
	if cfg.Remora.Monitoring.UserLogin.Interval.Duration <= 0 || cfg.Remora.Monitoring.UserLogin.User == "" {
		t.Fatalf("sample user-login monitor = %+v", cfg.Remora.Monitoring.UserLogin)
	}
	if cfg.Jellyfin.Parameters["package-name"] != "jellyfin-remora" {
		t.Fatalf("sample parameters = %+v", cfg.Jellyfin.Parameters)
	}
	if len(cfg.Jellyfin.Env) != 0 {
		t.Fatalf("Windows sample must leave Jellyfin environment overrides commented: %+v", cfg.Jellyfin.Env)
	}
	if !cfg.Jellyfin.General.Paths.CachePath.Set || !cfg.Jellyfin.Branding.EnableSplashScreen.Set || !cfg.Jellyfin.Playback.Transcoding.TranscodePath.Set {
		t.Fatal("sample does not exercise all managed Jellyfin setting groups")
	}
	if !cfg.Jellyfin.Playback.Transcoding.TranscodePath.Null {
		t.Fatal("sample transcode path must use Jellyfin's portable default")
	}
	server := cfg.Jellyfin.Networking.ServerAddressSettings
	if !server.LocalHTTPPortConfigured || !server.LocalHTTPSPortConfigured || !server.EnableHTTPSConfigured || !server.BaseURLConfigured || !server.BindToLocalNetworkAddress.Set {
		t.Fatalf("sample server address settings = %+v", server)
	}
}

func TestWindowsRejectsInvalidJellyfinEnvironment(t *testing.T) {
	base, err := Load(filepath.Join("..", "..", "sample", "config-windows.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{{"": "value"}, {"BAD=NAME": "value"}, {"GOOD": "bad\x00value"}} {
		candidate := *base
		candidate.Jellyfin.Env = env
		if err := candidate.Validate(); err == nil || !strings.Contains(err.Error(), "jellyfin.env") {
			t.Fatalf("environment %q validation error = %v", env, err)
		}
	}
}

func TestWindowsPhysicalVolumeConfiguration(t *testing.T) {
	root := t.TempDir()
	body := `config-version: 2
restapi:
  listen: 127.0.0.1
remora:
  data-dir: default
jellyfin:
  path: C:\tools\jellyfin
  data-dir: D:\jellyfin\data
  config-dir: D:\jellyfin\config
  cache-dir: D:\jellyfin\cache
  log-dir: D:\jellyfin\log
disk:
  - type: physical
    volume-guid: '\\?\Volume{8d63858a-6ae4-44bf-b0ed-c3a00f467af4}\'
    volume-label: STORAGE
    filesystem: NTFS
    target: 'D:\'
    probe-path: 'D:\jellyfin'
    permission: rw
`
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Disks[0]; got.VolumeLabel != "STORAGE" || got.Filesystem != "NTFS" || got.ProbePath != `D:\jellyfin` || got.VolumeGUID != `\\?\Volume{8d63858a-6ae4-44bf-b0ed-c3a00f467af4}\` {
		t.Fatalf("Windows volume fields = %+v", got)
	}
}

func TestWindowsRejectsCaseCollidingEnvironmentNames(t *testing.T) {
	cfg := Config{Jellyfin: JellyfinConfig{Env: map[string]string{"PATH": `C:\\Windows`, "Path": `C:\\Tools`}}, RESTAPI: RESTAPIConfig{NamedPipe: `\\.\pipe\jellyfin-remora`}}
	if err := validatePlatformConfig(&cfg); err == nil || !strings.Contains(err.Error(), "case-colliding") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestWindowsRejectsUnixPhysicalIdentity(t *testing.T) {
	cfg := Config{
		ConfigVersion: CurrentVersion,
		RESTAPI:       RESTAPIConfig{Listen: "127.0.0.1", Port: 8095},
		Remora: RemoraConfig{
			ServerStartTimeout: Duration{Duration: 1},
			ServerStopTimeout:  Duration{Duration: 1},
			HeartbeatInterval:  Duration{Duration: 1},
			IOTimeout:          Duration{Duration: 1},
			Logs:               LogConfig{Level: "info"},
		},
		Jellyfin: JellyfinConfig{Path: `C:\jellyfin.exe`, DataDir: `D:\data`, ConfigDir: `D:\config`, CacheDir: `D:\cache`, LogDir: `D:\log`},
		Disks:    []DiskConfig{{Type: "physical", UUID: "darwin-uuid", Target: `D:\`, Permission: "rw", FailureThreshold: 1}},
	}
	cfg.defaults()
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires volume-guid on Windows") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestWindowsNFSValidation(t *testing.T) {
	cfg := Config{
		ConfigVersion: CurrentVersion,
		RESTAPI:       RESTAPIConfig{Listen: "127.0.0.1", Port: 8095},
		Remora: RemoraConfig{
			ServerStartTimeout: Duration{Duration: 1}, ServerStopTimeout: Duration{Duration: 1},
			HeartbeatInterval: Duration{Duration: 1}, IOTimeout: Duration{Duration: 1},
			Logs: LogConfig{Level: "info"},
		},
		Jellyfin: JellyfinConfig{Path: `C:\jellyfin.exe`, DataDir: `Z:\data`, ConfigDir: `Z:\config`, CacheDir: `Z:\cache`, LogDir: `Z:\log`},
		Disks:    []DiskConfig{{Type: "nfs", Device: "server:/media", Target: `Z:\`, Permission: "rw", FailureThreshold: 1}},
	}
	cfg.defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Disks[0].Password = "plaintext"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not YAML") {
		t.Fatalf("NFS plaintext credential error = %v", err)
	}
}

func TestWindowsSMBRejectsPlaintextCredentials(t *testing.T) {
	cfg := Config{
		ConfigVersion: CurrentVersion,
		RESTAPI:       RESTAPIConfig{Listen: "127.0.0.1", Port: 8095},
		Remora: RemoraConfig{
			ServerStartTimeout: Duration{Duration: 1}, ServerStopTimeout: Duration{Duration: 1},
			HeartbeatInterval: Duration{Duration: 1}, IOTimeout: Duration{Duration: 1},
			Logs: LogConfig{Level: "info"},
		},
		Jellyfin: JellyfinConfig{Path: `C:\jellyfin.exe`, DataDir: `F:\data`, ConfigDir: `F:\config`, CacheDir: `F:\cache`, LogDir: `F:\log`},
		Disks: []DiskConfig{{
			Type: "smb", Device: "//server/share", Target: `F:\`, Credential: "windows-credential-manager",
			Permission: "rw", FailureThreshold: 1,
		}},
	}
	cfg.defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*DiskConfig){
		func(disk *DiskConfig) { disk.User = "nas" },
		func(disk *DiskConfig) { disk.Password = "secret" },
	} {
		candidate := cfg
		candidate.Disks = append([]DiskConfig(nil), cfg.Disks...)
		mutate(&candidate.Disks[0])
		if err := candidate.Validate(); err == nil || !strings.Contains(err.Error(), "not YAML user/password") {
			t.Fatalf("Validate() error = %v", err)
		}
	}
}
