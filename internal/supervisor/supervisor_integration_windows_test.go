//go:build windows

package supervisor

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfin"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"github.com/ChowDPa02K/jellyfin-remora/internal/procmanager"
	"golang.org/x/sys/windows"
)

type healthyWindowsStorage struct{}

func (healthyWindowsStorage) CheckDisk(_ context.Context, index int) model.StorageResult {
	return model.StorageResult{Index: index, Healthy: true, Mounted: true, Writable: true, Reachable: true}
}

func (healthyWindowsStorage) CheckPaths(context.Context) []model.StorageResult { return nil }

func TestWindowsHungHealthEndpointForcesJobRestart(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "fake-jellyfin.exe")
	build := exec.Command("go", "build", "-o", executable, "../testdata/fakejellyfin")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake Jellyfin: %v: %s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	healthFile := filepath.Join(directory, "health")
	childPIDFile := filepath.Join(directory, "child.pid")
	if err := os.WriteFile(healthFile, []byte("healthy\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Remora: config.RemoraConfig{
			ServerStartTimeout:  config.Duration{Duration: time.Second},
			ServerStopTimeout:   config.Duration{Duration: 300 * time.Millisecond},
			HeartbeatInterval:   config.Duration{Duration: 25 * time.Millisecond},
			HealthAPIHeartbeat:  1,
			IOTimeout:           config.Duration{Duration: 100 * time.Millisecond},
			RecoverySuccesses:   1,
			APIFailureThreshold: 2,
			DataDir:             directory,
		},
		Jellyfin: config.JellyfinConfig{
			Path:      executable,
			DataDir:   filepath.Join(directory, "data"),
			ConfigDir: filepath.Join(directory, "config"),
			CacheDir:  filepath.Join(directory, "cache"),
			LogDir:    filepath.Join(directory, "log"),
			Parameters: map[string]any{
				"fakeport":     port,
				"healthfile":   healthFile,
				"childpidfile": childPIDFile,
			},
			Networking: config.NetworkingConfig{ServerAddressSettings: config.ServerAddressSettings{LocalHTTPPort: port}},
		},
	}
	for _, path := range []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir} {
		if err := os.MkdirAll(path, 0750); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := procmanager.New(cfg, platform.New(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop(context.Background(), true, time.Second) })
	sup := New(cfg, manager, healthyWindowsStorage{}, jellyfin.New(cfg.JellyfinURL(), 100*time.Millisecond), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	waitWindowsSupervisorState(t, sup, model.StateRunning, 10*time.Second)
	oldPID := sup.Status().PID
	oldChildPID := waitWindowsChildPID(t, childPIDFile)
	if err := os.WriteFile(healthFile, []byte("hang\n"), 0600); err != nil {
		t.Fatal(err)
	}
	waitWindowsSupervisorState(t, sup, model.StateRestartBackoff, 5*time.Second)
	waitWindowsProcessGone(t, oldPID)
	waitWindowsProcessGone(t, oldChildPID)
	if err := os.WriteFile(healthFile, []byte("healthy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	waitWindowsSupervisorState(t, sup, model.StateRunning, 10*time.Second)
	if newPID := sup.Status().PID; newPID == 0 || newPID == oldPID {
		t.Fatalf("hung Jellyfin was not replaced: old=%d new=%d", oldPID, newPID)
	}
}

func waitWindowsSupervisorState(t *testing.T, supervisor *Supervisor, state model.State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if supervisor.Status().State == state {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("state=%s, want %s; status=%+v", supervisor.Status().State, state, supervisor.Status())
}

func waitWindowsChildPID(t *testing.T, path string) int {
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
	t.Fatal("fake Jellyfin child PID was not recorded")
	return 0
}

func waitWindowsProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			return
		}
		windows.CloseHandle(handle)
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d survived forced Job Object termination", pid)
}
