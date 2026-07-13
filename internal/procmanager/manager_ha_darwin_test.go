//go:build darwin

package procmanager

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
)

func buildHAFake(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "fake-jellyfin")
	cmd := exec.Command("go", "build", "-o", exe, "../testdata/fakejellyfin")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake Jellyfin: %v: %s", err, out)
	}
	return exe
}

func haManager(t *testing.T, exe string, parameters map[string]any) (*Manager, *config.Config) {
	t.Helper()
	d := t.TempDir()
	cfg := &config.Config{Remora: config.RemoraConfig{DataDir: d}, Jellyfin: config.JellyfinConfig{Path: exe, DataDir: filepath.Join(d, "data"), ConfigDir: filepath.Join(d, "config"), CacheDir: filepath.Join(d, "cache"), LogDir: filepath.Join(d, "log"), Parameters: parameters}}
	for _, p := range []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir} {
		if err := os.MkdirAll(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	pm, err := New(cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return pm, cfg
}

func waitForMatchingProcesses(t *testing.T, executable string, args []string, want int) {
	t.Helper()
	backend := platform.New()
	deadline := time.Now().Add(3 * time.Second)
	var last []platform.ProcessInfo
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = backend.FindProcesses(context.Background(), executable, args)
		if lastErr == nil && len(last) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("matching processes=%d, want=%d, err=%v", len(last), want, lastErr)
}

func TestAdoptsExactlyOneMatchingProcess(t *testing.T) {
	exe := buildHAFake(t)
	owner, cfg := haManager(t, exe, map[string]any{"fakeport": 0})
	if err := owner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Stop(context.Background(), true, time.Second) })
	waitForMatchingProcesses(t, owner.Executable(), owner.Arguments(), 1)
	adopter, err := New(cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := adopter.Adopt(context.Background())
	if err != nil || !adopted || adopter.PID() != owner.PID() {
		t.Fatalf("adopted=%v adopter_pid=%d owner_pid=%d err=%v", adopted, adopter.PID(), owner.PID(), err)
	}
	if err := adopter.Stop(context.Background(), false, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptionRejectsDuplicateProcesses(t *testing.T) {
	exe := buildHAFake(t)
	one, cfg := haManager(t, exe, map[string]any{"fakeport": 0})
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
	waitForMatchingProcesses(t, one.Executable(), one.Arguments(), 2)
	observer, err := New(cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if adopted, err := observer.Adopt(context.Background()); err == nil || adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
}

func TestStalePIDFileNeverAuthorizesKillingUnrelatedProcess(t *testing.T) {
	exe := buildHAFake(t)
	pm, cfg := haManager(t, exe, map[string]any{"fakeport": 0})
	if err := os.WriteFile(filepath.Join(cfg.Remora.DataDir, "jellyfin.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	adopted, err := pm.Adopt(context.Background())
	if err != nil || adopted {
		t.Fatalf("adopted=%v err=%v", adopted, err)
	}
	if err := pm.Stop(context.Background(), true, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(os.Getpid(), 0); err != nil {
		t.Fatalf("test process was affected: %v", err)
	}
}

func TestStopKillsManagedProcessGroupDescendant(t *testing.T) {
	exe := buildHAFake(t)
	childFile := filepath.Join(t.TempDir(), "child.pid")
	pm, _ := haManager(t, exe, map[string]any{"fakeport": 0, "childpidfile": childFile})
	if err := pm.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pm.Stop(context.Background(), true, time.Second) })
	var childPID int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(childFile); err == nil {
			childPID, _ = strconv.Atoi(string(bytesTrimSpace(b)))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("fake Jellyfin did not create descendant")
	}
	if err := pm.Stop(context.Background(), false, time.Second); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived process-group stop", childPID)
}

func bytesTrimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\n' || b[start] == '\r' || b[start] == '\t') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\n' || b[end-1] == '\r' || b[end-1] == '\t') {
		end--
	}
	return b[start:end]
}
