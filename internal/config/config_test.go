package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadDefaultsAndLegacyHeartbeatAliases(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "config.yml")
	root := filepath.ToSlash(d)
	yaml := fmt.Sprintf(`restapi:
  listen: 127.0.0.1
remora:
  health-api-hearbeat: 7
jellyfin:
  path: /Applications/Jellyfin.app/Contents/MacOS
  run-as-user: nobody
  data-dir: %s/jf/data
  config-dir: %s/jf/config
  cache-dir: %s/jf/cache
  log-dir: %s/jf/log
disk:
  - type: SMB
    device: //nas/share
    target: '%s'
    permission: r
    hearbeat: 4
`, root, root, root, root, testSMBTarget(root))
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Remora.HealthAPIHeartbeat != 7 {
		t.Fatalf("heartbeat=%d", c.Remora.HealthAPIHeartbeat)
	}
	if c.Disks[0].Heartbeat != 4 || c.Disks[0].FailureThreshold != 1 || c.Disks[0].Type != "smb" {
		t.Fatalf("disk defaults: %#v", c.Disks[0])
	}
	if c.Remora.ServerStartTimeout.Duration != 30*time.Second {
		t.Fatalf("start timeout=%s", c.Remora.ServerStartTimeout.Duration)
	}
	if !c.Remora.Monitoring.Database.IsEnabled() || c.Remora.Monitoring.Database.ConfirmationWindow.Duration != 5*time.Minute || c.Remora.Monitoring.Database.FailureThreshold != 1 {
		t.Fatalf("database monitoring defaults: %#v", c.Remora.Monitoring.Database)
	}
	if c.JellyfinURL() != "http://127.0.0.1:8096" {
		t.Fatalf("url=%s", c.JellyfinURL())
	}
}

func TestRejectsNonLoopbackControlAPI(t *testing.T) {
	c := Config{RESTAPI: RESTAPIConfig{Listen: "0.0.0.0", Port: 8095}, Remora: RemoraConfig{ServerStartTimeout: Duration{time.Second}, ServerStopTimeout: Duration{time.Second}, HeartbeatInterval: Duration{time.Second}, IOTimeout: Duration{time.Second}, Logs: LogConfig{Level: "info"}}, Jellyfin: JellyfinConfig{Path: "/x", DataDir: "/d", ConfigDir: "/c", CacheDir: "/k", LogDir: "/l", RunAsUser: "nobody"}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected non-loopback validation error")
	}
}

func TestDurationDaysAndWeeks(t *testing.T) {
	for input, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "1w": 7 * 24 * time.Hour} {
		var d Duration
		if err := d.UnmarshalYAML(&yaml.Node{Kind: yaml.ScalarNode, Value: input}); err != nil {
			t.Fatal(err)
		}
		if d.Duration != want {
			t.Fatalf("%s=%s", input, d.Duration)
		}
	}
}

func TestRejectsUnknownTopLevelKey(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "bad.yml")
	body := `config-version: 1
unknown-safety-setting: true
jellyfin:
  path: /app
  data-dir: /data
  config-dir: /config
  cache-dir: /cache
  log-dir: /log
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected strict YAML error")
	}
}

func TestRejectsIncompleteInitAndWatchdog(t *testing.T) {
	base := Config{ConfigVersion: CurrentVersion, RESTAPI: RESTAPIConfig{Listen: "127.0.0.1", Port: 8095}, Remora: RemoraConfig{ServerStartTimeout: Duration{time.Second}, ServerStopTimeout: Duration{time.Second}, HeartbeatInterval: Duration{time.Second}, IOTimeout: Duration{time.Second}, Logs: LogConfig{Level: "info"}}, Jellyfin: JellyfinConfig{Path: "/x", DataDir: "/d", ConfigDir: "/c", CacheDir: "/k", LogDir: "/l", RunAsUser: "nobody"}}
	base.Init.User = "admin"
	if err := base.Validate(); err == nil {
		t.Fatal("expected incomplete init error")
	}
	base.Init = InitConfig{}
	base.Remora.UserLoginWatchdog = UserLoginWatchdogConfig{Enabled: true, Heartbeat: 1, User: "remora"}
	if err := base.Validate(); err == nil {
		t.Fatal("expected incomplete watchdog error")
	}
}

func TestRejectsInvalidJellyfinEnvironment(t *testing.T) {
	base := Config{ConfigVersion: CurrentVersion, RESTAPI: RESTAPIConfig{Listen: "127.0.0.1", Port: 8095}, Remora: RemoraConfig{ServerStartTimeout: Duration{time.Second}, ServerStopTimeout: Duration{time.Second}, HeartbeatInterval: Duration{time.Second}, IOTimeout: Duration{time.Second}, Logs: LogConfig{Level: "info"}}, Jellyfin: JellyfinConfig{Path: "/x", DataDir: "/d", ConfigDir: "/c", CacheDir: "/k", LogDir: "/l", RunAsUser: "nobody"}}
	base.defaults()
	for _, env := range []map[string]string{{"": "value"}, {"BAD=NAME": "value"}, {"GOOD": "bad\x00value"}} {
		candidate := base
		candidate.Jellyfin.Env = env
		if err := candidate.Validate(); err == nil || !strings.Contains(err.Error(), "jellyfin.env") {
			t.Fatalf("environment %q validation error = %v", env, err)
		}
	}
}

func TestLoadManagedJellyfinSettingsTracksNullAndConfiguredValues(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	body := fmt.Sprintf(`config-version: 1
restapi:
  listen: 127.0.0.1
jellyfin:
  path: /app
  run-as-user: nobody
  data-dir: %s/data
  config-dir: %s/config
  cache-dir: %s/cache
  log-dir: %s/log
  general:
    paths:
      cache-path: null
      metadata-path: default
    performance:
      parallel-library-scan-tasks-limit: 1
      parallel-image-encoding-limit: null
  branding:
    login-disclaimer: Welcome
  networking:
    server-address-settings:
      local-http-port-number: 8097
      base-url: null
      bind-to-local-network-address: [127.0.0.1]
    ip-protocols:
      enable-ipv6: true
`, root, root, root, root)
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Jellyfin.HasManagedSettings() || !cfg.Jellyfin.General.Paths.CachePath.Null {
		t.Fatalf("managed settings not retained: %#v", cfg.Jellyfin.General)
	}
	if limit := cfg.Jellyfin.General.Performance.ParallelImageEncodingLimit; !limit.Set || !limit.Null {
		t.Fatalf("null performance limit not retained: %#v", limit)
	}
	address := cfg.Jellyfin.Networking.ServerAddressSettings
	if !address.LocalHTTPPortConfigured || address.LocalHTTPPort != 8097 || !address.BaseURLConfigured || !address.BaseURLNull {
		t.Fatalf("address settings = %#v", address)
	}
	if !address.BindToLocalNetworkAddress.Set || len(address.BindToLocalNetworkAddress.Value) != 1 {
		t.Fatalf("bind settings = %#v", address.BindToLocalNetworkAddress)
	}
}

func TestTCPControlCompatibilityDefaultAndExplicitDisable(t *testing.T) {
	if !(RESTAPIConfig{}).TCPControlEnabled() {
		t.Fatal("version 2 configuration without tcp-enabled lost its compatibility listener")
	}
	disabled := RESTAPIConfig{TCPEnabled: Optional[bool]{Set: true, Value: false}}
	if disabled.TCPControlEnabled() {
		t.Fatal("explicit tcp-enabled: false was ignored")
	}
}
