package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/databasemonitor"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfin"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfinconfig"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
)

type stateProcess struct {
	running         bool
	state           string
	forceStop       bool
	startErr        error
	stopErr         error
	stopCalls       int
	startCalls      int
	duplicateStarts int
	started         time.Time
	ports           []int
}

func TestFirstStartInitializationBacksOffAndOpensCircuit(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/System/Info/Public":
			complete := false
			_ = json.NewEncoder(w).Encode(jellyfin.PublicInfo{StartupWizardCompleted: &complete})
		case "/Startup/User":
			attempts++
			http.Error(w, "persistent setup failure", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	process := &stateProcess{running: true, started: time.Now().Add(-time.Minute)}
	s := stateSupervisor(t, process)
	s.client = jellyfin.New(server.URL, time.Second)
	s.cfg.Init = config.InitConfig{User: "admin", Password: "secret"}
	s.wizardIncompleteRuns = 3
	for want := 1; want <= 5; want++ {
		s.nextInitialization = time.Time{}
		s.reconcile(context.Background())
		if attempts != want {
			t.Fatalf("attempts=%d, want %d", attempts, want)
		}
		if want < 5 {
			s.reconcile(context.Background())
			if attempts != want {
				t.Fatalf("initialization ignored backoff after attempt %d", want)
			}
		}
	}
	if !s.processFailed || process.running || s.Status().State != model.StateProcessFailed {
		t.Fatalf("failed=%t running=%t state=%s", s.processFailed, process.running, s.Status().State)
	}
	reply := make(chan error, 1)
	s.handle(Request{Action: ActionStart, Reply: reply})
	if err := <-reply; err != nil {
		t.Fatal(err)
	}
	if s.processFailed || s.initializationFails != 0 || !s.nextInitialization.IsZero() {
		t.Fatal("administrative start did not reset initialization circuit")
	}
}

func TestReconcilePerformsOneHealthRequestPerTick(t *testing.T) {
	healthRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			healthRequests++
			w.WriteHeader(http.StatusOK)
		case "/System/Info/Public":
			complete := true
			_ = json.NewEncoder(w).Encode(jellyfin.PublicInfo{StartupWizardCompleted: &complete})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	process := &stateProcess{running: true, started: time.Now().Add(-time.Minute)}
	s := stateSupervisor(t, process)
	s.client = jellyfin.New(server.URL, time.Second)
	s.tick = 1
	s.reconcile(context.Background())
	if healthRequests != 1 {
		t.Fatalf("health requests=%d, want 1", healthRequests)
	}
}

func TestManualAndStorageStopFailuresAreVisibleAndBackedOff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*Supervisor)
		detail string
	}{
		{name: "manual", setup: func(s *Supervisor) {
			s.status.ManualStop = true
			s.status.DesiredState = model.DesiredStopped
		}, detail: "manual stop failed"},
		{name: "storage", setup: func(s *Supervisor) {
			s.status.Storage = []model.StorageResult{{Healthy: false, Fatal: true}}
		}, detail: "storage fence stop failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			process := &stateProcess{running: true, stopErr: errors.New("signal timeout"), started: time.Now()}
			s := stateSupervisor(t, process)
			tc.setup(s)
			s.reconcile(context.Background())
			firstRetry := s.nextStopRetry
			s.reconcile(context.Background())
			if process.stopCalls != 1 || !s.nextStopRetry.Equal(firstRetry) {
				t.Fatalf("stop retry ignored backoff: calls=%d next=%s", process.stopCalls, s.nextStopRetry)
			}
			for range 4 {
				s.nextStopRetry = time.Time{}
				s.reconcile(context.Background())
			}
			status := s.Status()
			if process.stopCalls != 5 || !s.processFailed || status.State != model.StateProcessFailed {
				t.Fatalf("calls=%d failed=%t state=%s", process.stopCalls, s.processFailed, status.State)
			}
			if !strings.Contains(status.LastError, tc.detail) || !strings.Contains(status.LastError, "retrying in") {
				t.Fatalf("stop failure is not observable: %q", status.LastError)
			}
		})
	}
}

func TestFrozenStateFormatAndForwardCompatibleManualStop(t *testing.T) {
	data, damage := encodeState(model.Status{
		ManualStop: true,
		Jellyfin:   model.HealthResult{Healthy: true},
		Storage:    []model.StorageResult{{Healthy: false}},
	}, true)
	if got, want := string(data), "0\n2\n1\n1\n"; got != want || damage != 2 {
		t.Fatalf("state=%q damage=%d, want %q damage=2", got, damage, want)
	}
	path := filepath.Join(t.TempDir(), "jellyfin.state")
	if err := os.WriteFile(path, append(data, []byte("future-field\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	state := readPersistedState(path)
	if !state.ManualStop || !state.DatabaseDamaged {
		t.Fatalf("reader rejected compatible state fields: %+v", state)
	}
	if err := os.WriteFile(path, []byte("0\n2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if state := readPersistedState(path); state.ManualStop || state.DatabaseDamaged {
		t.Fatalf("truncated state file enabled a fence: %+v", state)
	}
}

func TestDatabaseCorruptionEvidenceAndFailedHealthLatchesFence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			http.Error(w, "Unhealthy", http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	process := &stateProcess{running: true, started: time.Now().Add(-time.Minute)}
	s := stateSupervisor(t, process)
	s.client = jellyfin.New(server.URL, time.Second)
	s.cfg.Remora.Monitoring.Database.ConfirmationWindow = config.Duration{Duration: time.Minute}
	s.cfg.Remora.Monitoring.Database.FailureThreshold = 1
	detector := &databasemonitor.Detector{}
	_, _ = detector.Write([]byte("Microsoft.Data.Sqlite.SqliteException: SQLite Error 11: 'database disk image is malformed'.\n"))
	s.SetDatabaseDamageSource(detector)
	s.reconcile(context.Background())
	status := s.Status()
	if process.running || status.State != model.StateDatabaseDamaged || !status.Database.Damaged || !s.databaseDamaged {
		t.Fatalf("running=%t state=%s database=%+v latched=%t", process.running, status.State, status.Database, s.databaseDamaged)
	}

	reply := make(chan error, 1)
	s.handle(Request{Action: ActionStart, Reply: reply})
	if err := <-reply; err != nil {
		t.Fatal(err)
	}
	if s.databaseDamaged || s.Status().Database.Damaged {
		t.Fatal("explicit start did not acknowledge the database fence")
	}
}

func TestDatabaseCorruptionLogWithoutAPIFailureRemainsSuspected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/System/Info/Public":
			complete := true
			_ = json.NewEncoder(w).Encode(jellyfin.PublicInfo{StartupWizardCompleted: &complete})
		case "/Users":
			_ = json.NewEncoder(w).Encode([]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	process := &stateProcess{running: true, started: time.Now().Add(-time.Minute)}
	s := stateSupervisor(t, process)
	s.client = jellyfin.New(server.URL, time.Second)
	s.setAPIKey("api-key")
	s.cfg.Remora.Monitoring.Database.ConfirmationWindow = config.Duration{Duration: time.Minute}
	s.cfg.Remora.Monitoring.Database.FailureThreshold = 1
	detector := &databasemonitor.Detector{}
	_, _ = detector.Write([]byte("SQLite Error 11: database disk image is malformed\n"))
	s.SetDatabaseDamageSource(detector)
	s.reconcile(context.Background())
	status := s.Status()
	if !process.running || status.State != model.StateDegraded || status.Database.Damaged || !status.Database.Suspected {
		t.Fatalf("running=%t state=%s database=%+v", process.running, status.State, status.Database)
	}
}

func TestDatabaseCorruptionAndDatabaseBackedAPIFailureLatchesFence(t *testing.T) {
	detector := &databasemonitor.Detector{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/System/Info/Public":
			complete := true
			_ = json.NewEncoder(w).Encode(jellyfin.PublicInfo{StartupWizardCompleted: &complete})
		case "/Users":
			_, _ = detector.Write([]byte("SQLite Error 11: database disk image is malformed\n"))
			http.Error(w, "SQLite Error 11", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	process := &stateProcess{running: true, started: time.Now().Add(-time.Minute)}
	s := stateSupervisor(t, process)
	s.client = jellyfin.New(server.URL, time.Second)
	s.setAPIKey("api-key")
	s.cfg.Remora.Monitoring.Database.ConfirmationWindow = config.Duration{Duration: time.Minute}
	s.cfg.Remora.Monitoring.Database.FailureThreshold = 1
	s.SetDatabaseDamageSource(detector)
	s.reconcile(context.Background())
	if process.running || s.Status().State != model.StateDatabaseDamaged || !s.Status().Database.Damaged {
		t.Fatalf("running=%t status=%+v", process.running, s.Status())
	}
}

func TestDatabaseAPIFailureWithoutCorruptionLogCannotLatchDamage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/System/Info/Public":
			complete := true
			_ = json.NewEncoder(w).Encode(jellyfin.PublicInfo{StartupWizardCompleted: &complete})
		case "/Users":
			http.Error(w, "unrelated server failure", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	process := &stateProcess{running: true, started: time.Now().Add(-time.Minute)}
	s := stateSupervisor(t, process)
	s.client = jellyfin.New(server.URL, time.Second)
	s.setAPIKey("api-key")
	s.cfg.Remora.Monitoring.Database.ConfirmationWindow = config.Duration{Duration: time.Minute}
	s.cfg.Remora.Monitoring.Database.FailureThreshold = 1
	s.SetDatabaseDamageSource(&databasemonitor.Detector{})
	s.reconcile(context.Background())
	status := s.Status()
	if !process.running || status.State != model.StateDegraded || status.Database.Damaged || !status.Database.Suspected {
		t.Fatalf("running=%t state=%s database=%+v", process.running, status.State, status.Database)
	}
}

func TestHealthySetupListenerDoesNotReportRunningBeforePublicInfoIsReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/System/Info/Public":
			http.Error(w, "core is still starting", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	process := &stateProcess{running: true, started: time.Now()}
	s := stateSupervisor(t, process)
	s.client = jellyfin.New(server.URL, time.Second)
	s.cfg.Remora.ServerStartTimeout = config.Duration{Duration: time.Minute}
	s.reconcile(context.Background())
	status := s.Status()
	if status.State != model.StateStarting {
		t.Fatalf("state=%s, want %s", status.State, model.StateStarting)
	}
	if status.Jellyfin.Healthy || !strings.Contains(status.Jellyfin.Error, "public information is unavailable") {
		t.Fatalf("application health=%+v", status.Jellyfin)
	}
}

func TestTransientPublicInfoFailureDoesNotRestartReadyServer(t *testing.T) {
	var publicCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/System/Info/Public":
			if publicCalls.Add(1) > 1 {
				http.Error(w, "server busy", http.StatusServiceUnavailable)
				return
			}
			complete := true
			_ = json.NewEncoder(w).Encode(jellyfin.PublicInfo{StartupWizardCompleted: &complete})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	process := &stateProcess{running: true, started: time.Now().Add(-time.Hour)}
	s := stateSupervisor(t, process)
	s.client = jellyfin.New(server.URL, time.Second)
	s.cfg.Remora.APIFailureThreshold = 1
	s.status.State = model.StateRunning
	s.reconcile(context.Background())
	if !s.applicationReady || s.Status().State != model.StateRunning {
		t.Fatalf("initial readiness=%t state=%s", s.applicationReady, s.Status().State)
	}
	s.reconcile(context.Background())
	if !process.running || s.Status().State != model.StateRunning || s.apiFailures != 0 {
		t.Fatalf("running=%t state=%s apiFailures=%d", process.running, s.Status().State, s.apiFailures)
	}
}

func TestReadyServerHealthFailureIgnoresRemainingStartupGrace(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			if !healthy.Load() {
				http.Error(w, "stalled", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/System/Info/Public":
			complete := true
			_ = json.NewEncoder(w).Encode(jellyfin.PublicInfo{StartupWizardCompleted: &complete})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	process := &stateProcess{running: true, started: time.Now()}
	s := stateSupervisor(t, process)
	s.client = jellyfin.New(server.URL, time.Second)
	s.cfg.Remora.ServerStartTimeout = config.Duration{Duration: time.Minute}
	s.cfg.Remora.HealthAPIHeartbeat = 1
	s.cfg.Remora.APIFailureThreshold = 1
	s.reconcile(context.Background())
	if !s.applicationReady || s.Status().State != model.StateRunning {
		t.Fatalf("initial readiness=%t state=%s", s.applicationReady, s.Status().State)
	}

	healthy.Store(false)
	s.reconcile(context.Background())
	if process.running || process.stopCalls != 1 || s.Status().State != model.StateRestartBackoff {
		t.Fatalf("running=%t stopCalls=%d state=%s", process.running, process.stopCalls, s.Status().State)
	}
}

func (p *stateProcess) Executable() string  { return "/fake/jellyfin" }
func (p *stateProcess) Arguments() []string { return nil }
func (p *stateProcess) PID() int {
	if p.running {
		return 42
	}
	return 0
}
func (p *stateProcess) StartedAt() time.Time                { return p.started }
func (p *stateProcess) Adopt(context.Context) (bool, error) { return p.running, nil }
func (p *stateProcess) Info(context.Context) (platform.ProcessInfo, bool) {
	if !p.running {
		return platform.ProcessInfo{}, false
	}
	return platform.ProcessInfo{PID: 42, PGID: 42, State: p.state, Ports: append([]int(nil), p.ports...)}, true
}
func (p *stateProcess) Start(context.Context) error {
	if p.running {
		p.duplicateStarts++
		return errors.New("duplicate process start")
	}
	p.running = true
	p.startCalls++
	p.started = time.Now()
	if p.startErr != nil {
		p.running = false
		return p.startErr
	}
	return nil
}
func (p *stateProcess) Stop(_ context.Context, force bool, _ time.Duration) error {
	p.stopCalls++
	p.forceStop = force
	if p.stopErr == nil {
		p.running = false
	}
	return p.stopErr
}

func TestSupervisorExitAfterManualStopDoesNotStopAgain(t *testing.T) {
	process := &stateProcess{}
	s := stateSupervisor(t, process)
	s.cfg.Remora.HeartbeatInterval = config.Duration{Duration: time.Second}
	s.mu.Lock()
	s.status.ManualStop = true
	s.status.DesiredState = model.DesiredStopped
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if process.stopCalls != 0 {
		t.Fatalf("already-stopped process received %d additional stop calls", process.stopCalls)
	}
	if status := s.Status(); status.State != model.StateStopped {
		t.Fatalf("state=%s, want %s", status.State, model.StateStopped)
	}
	for _, event := range s.Events(256) {
		if event.State == model.StateStopping {
			t.Fatalf("daemon exit emitted a duplicate STOPPING transition: %+v", event)
		}
	}
}
func (*stateProcess) WritePIDFile() error  { return nil }
func (*stateProcess) RemovePIDFile() error { return nil }

type stateStorage struct{}

type failingConfiguration struct{ err error }

func (f failingConfiguration) Reconcile() (jellyfinconfig.Result, error) {
	return jellyfinconfig.Result{}, f.err
}

func (stateStorage) CheckDisk(context.Context, int) model.StorageResult {
	return model.StorageResult{Healthy: true}
}
func (stateStorage) CheckPaths(context.Context) []model.StorageResult { return nil }

func stateSupervisor(t *testing.T, process *stateProcess) *Supervisor {
	t.Helper()
	d := t.TempDir()
	cfg := &config.Config{Remora: config.RemoraConfig{ServerStopTimeout: config.Duration{Duration: 10 * time.Millisecond}, DataDir: d, RecoverySuccesses: 1, HealthAPIHeartbeat: 1, APIFailureThreshold: 1}, Jellyfin: config.JellyfinConfig{DataDir: filepath.Join(d, "data")}}
	return New(cfg, process, stateStorage{}, jellyfin.New("http://127.0.0.1:1", time.Millisecond), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestStorageFenceRecoveryRejectsDegradedChecks(t *testing.T) {
	s := stateSupervisor(t, &stateProcess{})
	s.cfg.Remora.RecoverySuccesses = 3
	s.storageFenced = true
	s.healthyStorageRuns = 2
	s.mu.Lock()
	s.status.Storage = []model.StorageResult{{Healthy: false, Fatal: false, Message: "server unreachable"}}
	s.mu.Unlock()

	for range 3 {
		s.reconcile(context.Background())
	}
	if !s.storageFenced || s.healthyStorageRuns != 0 || s.Status().State != model.StateStorageFenced {
		t.Fatalf("degraded recovery cleared fence: fenced=%t healthy-runs=%d state=%s", s.storageFenced, s.healthyStorageRuns, s.Status().State)
	}
}

func TestStatusPortsAreObservedAndCleared(t *testing.T) {
	process := &stateProcess{running: true, ports: []int{8096}, started: time.Now().Add(-time.Minute)}
	s := stateSupervisor(t, process)
	if ports := s.Status().Ports; len(ports) != 0 {
		t.Fatalf("initial status guessed ports from configuration: %v", ports)
	}
	s.reconcile(context.Background())
	if ports := s.Status().Ports; len(ports) != 1 || ports[0] != 8096 {
		t.Fatalf("observed ports = %v", ports)
	}
	process.ports = nil
	s.reconcile(context.Background())
	if ports := s.Status().Ports; len(ports) != 0 {
		t.Fatalf("closed listener left stale ports: %v", ports)
	}
	process.running = false
	s.mu.Lock()
	s.status.Ports = []int{8096}
	s.mu.Unlock()
	s.reconcile(context.Background())
	if ports := s.Status().Ports; len(ports) != 0 {
		t.Fatalf("stopped process left stale ports: %v", ports)
	}
}

func TestUninterruptibleProcessIsForceKilledAfterTimeout(t *testing.T) {
	p := &stateProcess{running: true, state: "D", started: time.Now().Add(-time.Minute)}
	s := stateSupervisor(t, p)
	s.hungSince = time.Now().Add(-time.Second)
	s.reconcile(context.Background())
	if !p.forceStop || p.running || s.Status().State != model.StateRestartBackoff {
		t.Fatalf("force=%v running=%v state=%s", p.forceStop, p.running, s.Status().State)
	}
}

func TestUninterruptibleProcessKillFailureOpensProcessFailed(t *testing.T) {
	p := &stateProcess{running: true, state: "U", stopErr: errors.New("kill failed"), started: time.Now().Add(-time.Minute)}
	s := stateSupervisor(t, p)
	s.hungSince = time.Now().Add(-time.Second)
	s.reconcile(context.Background())
	if !p.forceStop || !s.processFailed || s.Status().State != model.StateProcessFailed {
		t.Fatalf("force=%v processFailed=%v state=%s", p.forceStop, s.processFailed, s.Status().State)
	}
}

func TestTransientUninterruptibleProcessRemainsRunningWhenHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/System/Info/Public":
			complete := true
			_ = json.NewEncoder(w).Encode(jellyfin.PublicInfo{StartupWizardCompleted: &complete})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := &stateProcess{running: true, state: "U", started: time.Now().Add(-time.Minute)}
	s := stateSupervisor(t, p)
	s.client = jellyfin.New(server.URL, time.Second)
	s.cfg.Remora.HealthAPIHeartbeat = 100
	s.tick = 1
	s.sessionsInitialized = true
	s.setAPIKey("api-key")
	s.status.State = model.StateRunning
	s.reconcile(context.Background())
	if p.forceStop || !p.running || s.Status().State != model.StateRunning || s.hungSince.IsZero() {
		t.Fatalf("force=%v running=%v state=%s hungSince=%s", p.forceStop, p.running, s.Status().State, s.hungSince)
	}
}

func TestConfigurationFailurePreventsJellyfinStart(t *testing.T) {
	p := &stateProcess{}
	s := stateSupervisor(t, p)
	s.configuration = failingConfiguration{err: errors.New("invalid XML")}
	s.reconcile(context.Background())
	if p.running || !s.processFailed || s.Status().State != model.StateProcessFailed {
		t.Fatalf("running=%v processFailed=%v state=%s", p.running, s.processFailed, s.Status().State)
	}
}

func TestNewProcessDoesNotInheritPreviousPIDHealth(t *testing.T) {
	p := &stateProcess{}
	s := stateSupervisor(t, p)
	s.status.Jellyfin = model.HealthResult{Healthy: true, StatusCode: 200, CheckedAt: time.Now()}
	s.crashes = []time.Time{time.Now()}
	s.reconcile(context.Background())
	status := s.Status()
	if !p.running || status.State != model.StateStarting {
		t.Fatalf("running=%v state=%s", p.running, status.State)
	}
	if status.Jellyfin.Healthy || !status.Jellyfin.CheckedAt.IsZero() {
		t.Fatalf("new process inherited stale health: %+v", status.Jellyfin)
	}
	if len(s.crashes) != 1 {
		t.Fatalf("startup cleared crash history before a new health check: %d", len(s.crashes))
	}
}

func TestEventHistoryIsBoundedAndOrdered(t *testing.T) {
	s := stateSupervisor(t, &stateProcess{})
	for i := 0; i < 300; i++ {
		state := model.StateRunning
		if i%2 == 0 {
			state = model.StateDegraded
		}
		s.transition(state, "test transition")
	}

	all := s.Events(999)
	if len(all) != 256 {
		t.Fatalf("events=%d, want 256", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].Sequence <= all[i-1].Sequence {
			t.Fatalf("event sequence is not increasing at %d: %d <= %d", i, all[i].Sequence, all[i-1].Sequence)
		}
	}
	last := s.Events(10)
	if len(last) != 10 || last[0].Sequence != all[len(all)-10].Sequence {
		t.Fatalf("unexpected event tail: %+v", last)
	}
}

func TestStatusIncludesProcessStartAndPlayingUsers(t *testing.T) {
	started := time.Now().Add(-time.Minute)
	p := &stateProcess{running: true, started: started}
	s := stateSupervisor(t, p)
	s.mu.Lock()
	s.status.PID = p.PID()
	s.status.Sessions = []model.Session{
		{User: "zoe", Status: "playing"},
		{User: "alice", Status: "paused"},
		{User: "zoe", Status: "paused"},
		{User: "ignored", Status: "idle"},
	}
	s.mu.Unlock()

	status := s.Status()
	if !status.ProcessStarted.Equal(started) || status.UptimeSeconds < 59 {
		t.Fatalf("unexpected process timing: started=%s uptime=%d", status.ProcessStarted, status.UptimeSeconds)
	}
	if len(status.PlayingUsers) != 2 || status.PlayingUsers[0] != "alice" || status.PlayingUsers[1] != "zoe" {
		t.Fatalf("playing users=%v", status.PlayingUsers)
	}
}

func TestManagementRedactsKeysAndStopsSessions(t *testing.T) {
	keys := []jellyfin.AuthenticationInfo{{AccessToken: "remora-secret", AppName: "Jellyfin Remora", IsActive: true}}
	stopped := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/Auth/Keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": keys})
		case r.Method == http.MethodPost && r.URL.Path == "/Auth/Keys":
			keys = append(keys, jellyfin.AuthenticationInfo{AccessToken: "new-secret", AppName: r.URL.Query().Get("app"), IsActive: true})
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/Auth/Keys/new-secret":
			keys = keys[:1]
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/Sessions":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"Id": "session-12345678", "UserName": "alice", "Client": "Web", "IsActive": true, "NowPlayingItem": map[string]string{"Name": "Movie"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/Sessions/session-12345678/Playing/Stop":
			stopped = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	s := stateSupervisor(t, &stateProcess{running: true})
	s.client = jellyfin.New(server.URL, time.Second)
	s.setAPIKey("remora-secret")

	listed, err := s.APIKeys(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ID == "remora-secret" || !listed[0].IsRemora {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	created, err := s.CreateAPIKey(context.Background(), "Living Room")
	if err != nil || created.Name != "Living Room" || created.ID == "new-secret" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	if err := s.DeleteAPIKey(context.Background(), created.ID[:8]); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAPIKey(context.Background(), listed[0].ID); err == nil {
		t.Fatal("supervisor active key deletion succeeded")
	}
	if err := s.StopSession(context.Background(), "session-"); err != nil || !stopped {
		t.Fatalf("stopped=%t err=%v", stopped, err)
	}
	types := map[string]bool{}
	for _, event := range s.Events(256) {
		types[event.Type] = true
	}
	for _, want := range []string{"api_key_created", "api_key_deleted", "session_stopped"} {
		if !types[want] {
			t.Fatalf("missing event %s: %+v", want, s.Events(256))
		}
	}
}
