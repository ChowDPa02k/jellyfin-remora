//go:build darwin

package supervisor

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfin"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"github.com/ChowDPa02K/jellyfin-remora/internal/procmanager"
)

func TestSupervisorStartsAndStopsHealthyJellyfin(t *testing.T) {
	d := t.TempDir()
	exe := filepath.Join(d, "fake-jellyfin")
	cmd := exec.Command("go", "build", "-o", exe, "../testdata/fakejellyfin")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake Jellyfin: %v: %s", err, b)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg := &config.Config{Remora: config.RemoraConfig{ServerStartTimeout: config.Duration{Duration: 2 * time.Second}, ServerStopTimeout: config.Duration{Duration: time.Second}, HeartbeatInterval: config.Duration{Duration: 20 * time.Millisecond}, HealthAPIHeartbeat: 1, IOTimeout: config.Duration{Duration: 500 * time.Millisecond}, RecoverySuccesses: 2, APIFailureThreshold: 2, DataDir: d}, Jellyfin: config.JellyfinConfig{Path: exe, DataDir: filepath.Join(d, "data"), ConfigDir: filepath.Join(d, "config"), CacheDir: filepath.Join(d, "cache"), LogDir: filepath.Join(d, "log"), Parameters: map[string]any{"fakeport": port}, Networking: config.NetworkingConfig{ServerAddressSettings: config.ServerAddressSettings{LocalHTTPPort: port}}}}
	for _, p := range []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir} {
		if err := os.MkdirAll(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	backend := platform.New()
	pm, err := procmanager.New(cfg, backend, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	sc := &toggledStorage{}
	sc.healthy.Store(true)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sup := New(cfg, pm, sc, jellyfin.New(cfg.JellyfinURL(), 500*time.Millisecond), logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitState(t, sup, model.StateRunning, 5*time.Second)
	if status := sup.Status(); status.Version != "12.0.0-test" || status.ServerName != "Fake Jellyfin" || status.Username == "" || status.UID < 0 {
		t.Fatalf("status metadata = %+v", status)
	}
	if err := sup.Submit(context.Background(), ActionStop, false); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, model.StateStopped, 3*time.Second)
}

func waitState(t *testing.T, s *Supervisor, want model.State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := s.Status().State; got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("state=%s, want %s; status=%+v", s.Status().State, want, s.Status())
}

type toggledStorage struct{ healthy atomic.Bool }

func (t *toggledStorage) CheckDisk(_ context.Context, index int) model.StorageResult {
	ok := t.healthy.Load()
	return model.StorageResult{Index: index, Type: "physical", Target: "/fake", Healthy: ok, Fatal: !ok, Mounted: ok, Writable: ok, Reachable: true, CheckedAt: time.Now()}
}

func (t *toggledStorage) CheckPaths(context.Context) []model.StorageResult { return nil }

func TestSupervisorFencesAndRecoversStorage(t *testing.T) {
	d := t.TempDir()
	exe := filepath.Join(d, "fake-jellyfin")
	cmd := exec.Command("go", "build", "-o", exe, "../testdata/fakejellyfin")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake Jellyfin: %v: %s", err, b)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	cfg := &config.Config{Remora: config.RemoraConfig{ServerStartTimeout: config.Duration{Duration: 2 * time.Second}, ServerStopTimeout: config.Duration{Duration: time.Second}, HeartbeatInterval: config.Duration{Duration: 20 * time.Millisecond}, HealthAPIHeartbeat: 1, IOTimeout: config.Duration{Duration: 500 * time.Millisecond}, RecoverySuccesses: 2, APIFailureThreshold: 2, DataDir: d}, Disks: []config.DiskConfig{{Type: "physical", Target: "/fake", Heartbeat: 1}}, Jellyfin: config.JellyfinConfig{Path: exe, DataDir: filepath.Join(d, "data"), ConfigDir: filepath.Join(d, "config"), CacheDir: filepath.Join(d, "cache"), LogDir: filepath.Join(d, "log"), Parameters: map[string]any{"fakeport": port}, Networking: config.NetworkingConfig{ServerAddressSettings: config.ServerAddressSettings{LocalHTTPPort: port}}}}
	backend := platform.New()
	pm, err := procmanager.New(cfg, backend, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	checker := &toggledStorage{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sup := New(cfg, pm, checker, jellyfin.New(cfg.JellyfinURL(), 500*time.Millisecond), logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitState(t, sup, model.StateStorageFenced, 2*time.Second)
	if sup.Status().PID != 0 {
		t.Fatal("Jellyfin started while storage was fenced")
	}
	checker.healthy.Store(true)
	waitState(t, sup, model.StateRunning, 5*time.Second)
	checker.healthy.Store(false)
	waitState(t, sup, model.StateStorageFenced, 3*time.Second)
	if sup.Status().PID != 0 {
		t.Fatal("Jellyfin remained running after storage damage")
	}
}

func newHAFixture(t *testing.T, parameters map[string]any) (*Supervisor, *toggledStorage, context.CancelFunc, <-chan error, *config.Config) {
	t.Helper()
	d := t.TempDir()
	exe := filepath.Join(d, "fake-jellyfin")
	cmd := exec.Command("go", "build", "-o", exe, "../testdata/fakejellyfin")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake Jellyfin: %v: %s", err, b)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if parameters == nil {
		parameters = map[string]any{}
	}
	parameters["fakeport"] = port
	cfg := &config.Config{Remora: config.RemoraConfig{ServerStartTimeout: config.Duration{Duration: 150 * time.Millisecond}, ServerStopTimeout: config.Duration{Duration: 500 * time.Millisecond}, HeartbeatInterval: config.Duration{Duration: 20 * time.Millisecond}, HealthAPIHeartbeat: 1, IOTimeout: config.Duration{Duration: 200 * time.Millisecond}, RecoverySuccesses: 3, APIFailureThreshold: 2, DataDir: d}, Disks: []config.DiskConfig{{Type: "physical", Target: "/fake", Heartbeat: 1}}, Jellyfin: config.JellyfinConfig{Path: exe, DataDir: filepath.Join(d, "data"), ConfigDir: filepath.Join(d, "config"), CacheDir: filepath.Join(d, "cache"), LogDir: filepath.Join(d, "log"), Parameters: parameters, Networking: config.NetworkingConfig{ServerAddressSettings: config.ServerAddressSettings{LocalHTTPPort: port}}}}
	for _, p := range []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir} {
		if err := os.MkdirAll(p, 0750); err != nil {
			t.Fatal(err)
		}
	}
	backend := platform.New()
	pm, err := procmanager.New(cfg, backend, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	// The ordinary deferred supervisor cancellation should stop the fixture.
	// Retain an exact executable+argv cleanup as a final test-process safety net
	// so a failed assertion or timing race cannot orphan fake Jellyfin locally.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		processes, findErr := backend.FindProcesses(ctx, pm.Executable(), pm.Arguments())
		if findErr != nil {
			return
		}
		for _, process := range processes {
			_ = backend.SignalGroup(process.PID, true)
		}
	})
	checker := &toggledStorage{}
	checker.healthy.Store(true)
	sup := New(cfg, pm, checker, jellyfin.New(cfg.JellyfinURL(), 200*time.Millisecond), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	return sup, checker, cancel, done, cfg
}

func TestRecoveryRequiresConfiguredHealthyStreak(t *testing.T) {
	sup, checker, cancel, done, _ := newHAFixture(t, nil)
	defer func() { cancel(); <-done }()
	waitState(t, sup, model.StateRunning, 5*time.Second)
	checker.healthy.Store(false)
	waitState(t, sup, model.StateStorageFenced, 3*time.Second)
	checker.healthy.Store(true)
	// Three 20 ms checks are required. The first healthy observation must not start.
	time.Sleep(25 * time.Millisecond)
	if st := sup.Status(); st.State != model.StateStorageFenced || st.PID != 0 {
		t.Fatalf("recovered too early: %+v", st)
	}
	waitState(t, sup, model.StateRunning, 5*time.Second)
}

func TestManualStopWinsAfterStorageRecovery(t *testing.T) {
	sup, checker, cancel, done, _ := newHAFixture(t, nil)
	defer func() { cancel(); <-done }()
	waitState(t, sup, model.StateRunning, 5*time.Second)
	checker.healthy.Store(false)
	waitState(t, sup, model.StateStorageFenced, 3*time.Second)
	if err := sup.Submit(context.Background(), ActionStop, false); err != nil {
		t.Fatal(err)
	}
	checker.healthy.Store(true)
	waitState(t, sup, model.StateStopped, 3*time.Second)
	time.Sleep(100 * time.Millisecond)
	if st := sup.Status(); !st.ManualStop || st.PID != 0 || st.State != model.StateStopped {
		t.Fatalf("manual stop did not win: %+v", st)
	}
}

func TestHealthFailureThresholdRestartsProcess(t *testing.T) {
	healthFile := filepath.Join(t.TempDir(), "health")
	if err := os.WriteFile(healthFile, []byte("healthy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sup, _, cancel, done, _ := newHAFixture(t, map[string]any{"healthfile": healthFile})
	defer func() { cancel(); <-done }()
	waitState(t, sup, model.StateRunning, 5*time.Second)
	oldPID := sup.Status().PID
	if err := os.WriteFile(healthFile, []byte("unhealthy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, model.StateRestartBackoff, 3*time.Second)
	if err := os.WriteFile(healthFile, []byte("healthy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, model.StateRunning, 5*time.Second)
	if newPID := sup.Status().PID; newPID == 0 || newPID == oldPID {
		t.Fatalf("health failure did not replace process: old=%d new=%d", oldPID, newPID)
	}
}

func TestTransientWizardFalseNeverRunsInitialization(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "startup-called")
	sup, _, cancel, done, _ := newHAFixture(t, map[string]any{"wizardfalsecount": 2, "startupmarker": marker})
	waitState(t, sup, model.StateRunning, 5*time.Second)
	cancel()
	<-done
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("startup API was called during ordinary boot: %v", err)
	}
	if sup.wizardIncompleteRuns != 0 {
		t.Fatalf("wizard incomplete streak was not reset: %d", sup.wizardIncompleteRuns)
	}
}

func TestCrashCircuitAndAdministrativeReset(t *testing.T) {
	sup, _, cancel, done, _ := newHAFixture(t, nil)
	waitState(t, sup, model.StateRunning, 5*time.Second)
	cancel()
	<-done
	for i := 0; i < 5; i++ {
		sup.recordCrash()
	}
	if !sup.processFailed {
		t.Fatal("five crashes did not open circuit")
	}
	reply := make(chan error, 1)
	sup.handle(Request{Action: ActionStart, Reply: reply})
	if err := <-reply; err != nil {
		t.Fatal(err)
	}
	if sup.processFailed || len(sup.crashes) != 0 {
		t.Fatalf("administrative start did not reset circuit: failed=%v crashes=%d", sup.processFailed, len(sup.crashes))
	}
}

func TestConcurrentControlRequestsRemainSerialized(t *testing.T) {
	sup, _, cancel, done, _ := newHAFixture(t, nil)
	defer func() { cancel(); <-done }()
	waitState(t, sup, model.StateRunning, 5*time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			action := ActionRestart
			if i%3 == 0 {
				action = ActionStop
			} else if i%3 == 1 {
				action = ActionStart
			}
			// Full-suite and race instrumentation can overlap these requests with
			// real process stop/start cycles. Keep the assertion bounded by the
			// fixture's existing lifecycle budget instead of a tighter scheduler-
			// dependent two-second deadline.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := sup.Submit(ctx, action, i%2 == 0); err != nil {
				t.Errorf("request %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if err := sup.Submit(context.Background(), ActionStop, true); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, model.StateStopped, 3*time.Second)
	if err := sup.Submit(context.Background(), ActionStart, false); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, model.StateRunning, 5*time.Second)
}

func TestUnexpectedSIGKILLIsObservedAsCrash(t *testing.T) {
	sup, _, cancel, done, _ := newHAFixture(t, nil)
	defer func() { cancel(); <-done }()
	waitState(t, sup, model.StateRunning, 5*time.Second)
	oldPID := sup.Status().PID
	if err := syscall.Kill(oldPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitState(t, sup, model.StateRestartBackoff, 3*time.Second)
	waitState(t, sup, model.StateRunning, 5*time.Second)
	if newPID := sup.Status().PID; newPID == oldPID || newPID == 0 {
		t.Fatalf("crashed process was not replaced: old=%d new=%d", oldPID, newPID)
	}
}
