package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"testing"
	"time"
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
    target: %s/share
    permission: r
    hearbeat: 4
`, root, root, root, root, root)
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
	if c.Disks[0].Heartbeat != 4 || c.Disks[0].Type != "smb" {
		t.Fatalf("disk defaults: %#v", c.Disks[0])
	}
	if c.Remora.ServerStartTimeout.Duration != 30*time.Second {
		t.Fatalf("start timeout=%s", c.Remora.ServerStartTimeout.Duration)
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
	base := Config{ConfigVersion: 1, RESTAPI: RESTAPIConfig{Listen: "127.0.0.1", Port: 8095}, Remora: RemoraConfig{ServerStartTimeout: Duration{time.Second}, ServerStopTimeout: Duration{time.Second}, HeartbeatInterval: Duration{time.Second}, IOTimeout: Duration{time.Second}, Logs: LogConfig{Level: "info"}}, Jellyfin: JellyfinConfig{Path: "/x", DataDir: "/d", ConfigDir: "/c", CacheDir: "/k", LogDir: "/l", RunAsUser: "nobody"}}
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
