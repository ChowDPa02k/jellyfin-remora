//go:build linux

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func TestGenerateLinuxSystemdServiceSupportsAdoptionAndOrdering(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "remora config.yaml")
	artifact, err := generatePlatformService(&config.Config{}, "/opt/Jellyfin Remora/jellyfin-remora", configPath)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(contents)
	for _, required := range []string{
		"Type=simple", "KillMode=process", "Conflicts=jellyfin.service",
		"RuntimeDirectory=jellyfin-remora", "StateDirectory=jellyfin-remora",
		"LogsDirectory=jellyfin-remora", "Before=umount.target shutdown.target",
		"StartLimitBurst=5", `ConditionPathExists="` + configPath + `"`,
		`ExecStart="/opt/Jellyfin Remora/jellyfin-remora" -c "` + configPath + `"`,
	} {
		if !strings.Contains(unit, required) {
			t.Errorf("generated unit omitted %q:\n%s", required, unit)
		}
	}
}

func TestLinuxServiceInstallAndStartAreIdempotent(t *testing.T) {
	root := t.TempDir()
	artifactPath := filepath.Join(root, "generated.service")
	if err := os.WriteFile(artifactPath, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldDirectory, oldChown, oldRun := linuxSystemdDirectory, linuxChown, runLinuxSystemd
	linuxSystemdDirectory = root
	linuxChown = func(string, int, int) error { return nil }
	var calls [][]string
	runLinuxSystemd = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	t.Cleanup(func() { linuxSystemdDirectory, linuxChown, runLinuxSystemd = oldDirectory, oldChown, oldRun })
	artifact := &serviceArtifact{Kind: "systemd service", Path: artifactPath}
	for _, value := range []string{"first", "second"} {
		if err := os.WriteFile(artifactPath, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := installPlatformService(artifact); err != nil {
			t.Fatal(err)
		}
	}
	written, err := os.ReadFile(filepath.Join(root, linuxServiceName))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "second" {
		t.Fatalf("installed unit = %q", written)
	}
	if err := startPlatformService(artifact); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"daemon-reload"}, {"enable", linuxServiceName}, {"daemon-reload"}, {"enable", linuxServiceName}, {"restart", linuxServiceName}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("systemctl calls = %#v", calls)
	}
}
