//go:build darwin || linux

package procmanager

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
)

func TestConfiguredEnvironmentReachesManagedProcess(t *testing.T) {
	t.Setenv("REMORA_ENV_INHERITED", "from-parent")
	t.Setenv("REMORA_ENV_OVERRIDE", "from-parent")
	t.Setenv("TERM", "inherited-terminal")

	directory := t.TempDir()
	executable := filepath.Join(directory, "fake-jellyfin")
	build := exec.Command("go", "build", "-o", executable, "../testdata/fakejellyfin")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake Jellyfin: %v: %s", err, output)
	}
	environmentFile := filepath.Join(directory, "environment.json")
	cfg := &config.Config{Jellyfin: config.JellyfinConfig{
		Path:      executable,
		DataDir:   filepath.Join(directory, "data"),
		ConfigDir: filepath.Join(directory, "config"),
		CacheDir:  filepath.Join(directory, "cache"),
		LogDir:    filepath.Join(directory, "log"),
		Env: map[string]string{
			"REMORA_ENV_OVERRIDE": "from-yaml",
			"REMORA_ENV_EMPTY":    "",
		},
		Parameters: map[string]any{"envfile": environmentFile, "fakeport": 0},
	}}
	manager, err := New(cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), true, time.Second) })

	deadline := time.Now().Add(5 * time.Second)
	var environment map[string]string
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(environmentFile)
		if readErr == nil && json.Unmarshal(data, &environment) == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for name, want := range map[string]string{
		"REMORA_ENV_INHERITED": "from-parent",
		"REMORA_ENV_OVERRIDE":  "from-yaml",
		"REMORA_ENV_EMPTY":     "",
		"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION": "1",
		"TERM": "inherited-terminal",
	} {
		if got, ok := environment[name]; !ok || got != want {
			t.Errorf("managed environment %s = %q (present=%t), want %q", name, got, ok, want)
		}
	}
}
