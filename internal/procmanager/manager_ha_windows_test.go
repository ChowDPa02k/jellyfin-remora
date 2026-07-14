//go:build windows

package procmanager

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"golang.org/x/sys/windows"
)

func buildWindowsHAFake(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "fake-jellyfin.exe")
	cmd := exec.Command("go", "build", "-o", exe, "../testdata/fakejellyfin")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake Jellyfin: %v: %s", err, out)
	}
	return exe
}

func windowsHAManager(t *testing.T, exe string, parameters map[string]any) (*Manager, *config.Config) {
	t.Helper()
	d := t.TempDir()
	cfg := &config.Config{Remora: config.RemoraConfig{DataDir: d}, Jellyfin: config.JellyfinConfig{Path: exe, DataDir: filepath.Join(d, "data"), ConfigDir: filepath.Join(d, "config"), CacheDir: filepath.Join(d, "cache"), LogDir: filepath.Join(d, "log"), Parameters: parameters}}
	for _, path := range []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir} {
		if err := os.MkdirAll(path, 0750); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := New(cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return manager, cfg
}

func waitForWindowsProcesses(t *testing.T, executable string, args []string, want int) {
	t.Helper()
	backend := platform.New()
	deadline := time.Now().Add(5 * time.Second)
	var found []platform.ProcessInfo
	var lastErr error
	for time.Now().Before(deadline) {
		found, lastErr = backend.FindProcesses(context.Background(), executable, args)
		if lastErr == nil && len(found) == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("matching processes=%d, want=%d, err=%v", len(found), want, lastErr)
}

func TestWindowsAdoptsExactlyOneMatchingProcess(t *testing.T) {
	exe := buildWindowsHAFake(t)
	owner, cfg := windowsHAManager(t, exe, map[string]any{"fakeport": 0})
	if err := owner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Stop(context.Background(), true, time.Second) })
	waitForWindowsProcesses(t, owner.Executable(), owner.Arguments(), 1)

	adopter, err := New(cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := adopter.Adopt(context.Background())
	if err != nil || !adopted || adopter.PID() != owner.PID() {
		t.Fatalf("adopted=%v adopter_pid=%d owner_pid=%d err=%v", adopted, adopter.PID(), owner.PID(), err)
	}
	if err := adopter.Stop(context.Background(), true, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsAdoptionRejectsDuplicateProcesses(t *testing.T) {
	exe := buildWindowsHAFake(t)
	one, cfg := windowsHAManager(t, exe, map[string]any{"fakeport": 0})
	two, err := New(cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := one.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := two.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = one.Stop(context.Background(), true, time.Second)
		_ = two.Stop(context.Background(), true, time.Second)
	})
	waitForWindowsProcesses(t, one.Executable(), one.Arguments(), 2)

	observer, err := New(cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if adopted, err := observer.Adopt(context.Background()); err == nil || adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
}

func TestWindowsStalePIDFileNeverAuthorizesKillingUnrelatedProcess(t *testing.T) {
	exe := buildWindowsHAFake(t)
	manager, cfg := windowsHAManager(t, exe, map[string]any{"fakeport": 0})
	if err := os.WriteFile(filepath.Join(cfg.Remora.DataDir, "jellyfin.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	adopted, err := manager.Adopt(context.Background())
	if err != nil || adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
	if err := manager.Stop(context.Background(), true, time.Second); err != nil {
		t.Fatal(err)
	}
	if handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(os.Getpid())); err != nil {
		t.Fatalf("test process was affected: %v", err)
	} else {
		windows.CloseHandle(handle)
	}
}

func TestWindowsStopKillsManagedJobDescendant(t *testing.T) {
	exe := buildWindowsHAFake(t)
	childFile := filepath.Join(t.TempDir(), "child.pid")
	manager, _ := windowsHAManager(t, exe, map[string]any{"fakeport": 0, "childpidfile": childFile})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), true, time.Second) })
	childPID := waitForWindowsChildPID(t, childFile)
	if err := manager.Stop(context.Background(), false, time.Second); err != nil {
		t.Fatal(err)
	}
	waitForWindowsProcessExit(t, childPID)
}

func waitForWindowsChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if contents, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(contents))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("fake Jellyfin did not create descendant")
	return 0
}

func waitForWindowsProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			return
		}
		windows.CloseHandle(handle)
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived managed stop", pid)
}
