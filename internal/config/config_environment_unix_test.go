//go:build darwin || linux

package config

import (
	"strings"
	"testing"
	"time"
)

func TestUnixRejectsInvalidJellyfinEnvironment(t *testing.T) {
	base := Config{ConfigVersion: CurrentVersion, RESTAPI: RESTAPIConfig{Listen: "127.0.0.1", Port: 8095}, Remora: RemoraConfig{ServerStartTimeout: Duration{time.Second}, ServerStopTimeout: Duration{time.Second}, HeartbeatInterval: Duration{time.Second}, IOTimeout: Duration{time.Second}, Logs: LogConfig{Level: "info"}}, Jellyfin: JellyfinConfig{Path: "/x", DataDir: "/d", ConfigDir: "/c", CacheDir: "/k", LogDir: "/l", RunAsUser: "nobody"}}
	base.defaults()
	assertInvalidJellyfinEnvironment(t, base)
}

func assertInvalidJellyfinEnvironment(t *testing.T, base Config) {
	t.Helper()
	for _, env := range []map[string]string{{"": "value"}, {"BAD=NAME": "value"}, {"GOOD": "bad\x00value"}} {
		candidate := base
		candidate.Jellyfin.Env = env
		if err := candidate.Validate(); err == nil || !strings.Contains(err.Error(), "jellyfin.env") {
			t.Fatalf("environment %q validation error = %v", env, err)
		}
	}
}
