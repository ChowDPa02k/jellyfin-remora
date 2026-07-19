//go:build linux

package procmanager

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
)

func buildLinuxHAFake(t *testing.T) string {
	t.Helper()
	if executable := os.Getenv("REMORA_TEST_FAKE_JELLYFIN"); executable != "" {
		return executable
	}
	executable := filepath.Join(t.TempDir(), "fake-jellyfin")
	command := exec.Command("go", "build", "-o", executable, "../testdata/fakejellyfin")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake Jellyfin: %v: %s", err, output)
	}
	return executable
}

func linuxHAManager(t *testing.T, executable string, parameters map[string]any) *Manager {
	t.Helper()
	directory := t.TempDir()
	cfg := &config.Config{
		Remora: config.RemoraConfig{DataDir: directory},
		Jellyfin: config.JellyfinConfig{
			Path: executable, DataDir: filepath.Join(directory, "data"),
			ConfigDir: filepath.Join(directory, "config"), CacheDir: filepath.Join(directory, "cache"),
			LogDir: filepath.Join(directory, "log"), Parameters: parameters,
		},
	}
	manager, err := New(cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestLinuxInterruptedSupervisorAdoptsWithoutDuplicate(t *testing.T) {
	executable := buildLinuxHAFake(t)
	owner := linuxHAManager(t, executable, map[string]any{"fakeport": 0})
	if err := owner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Stop(context.Background(), true, time.Second) })

	replacement, err := New(owner.cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := replacement.Adopt(context.Background())
	if err != nil || !adopted || replacement.PID() != owner.PID() {
		t.Fatalf("adopted=%t replacement=%d owner=%d err=%v", adopted, replacement.PID(), owner.PID(), err)
	}
	if err := replacement.Start(context.Background()); err == nil {
		t.Fatal("adopted manager started a duplicate Jellyfin process")
	}
	if err := replacement.Stop(context.Background(), true, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxForcedStopReapsManagedDescendant(t *testing.T) {
	executable := buildLinuxHAFake(t)
	childFile := filepath.Join(t.TempDir(), "child.pid")
	manager := linuxHAManager(t, executable, map[string]any{"fakeport": 0, "childpidfile": childFile})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), true, time.Second) })

	childPID := 0
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(childFile); err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("fake Jellyfin did not create its descendant")
	}
	if err := manager.Stop(context.Background(), true, time.Second); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("inspect managed descendant %d: %v", childPID, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("managed descendant %d survived forced group stop", childPID)
}
