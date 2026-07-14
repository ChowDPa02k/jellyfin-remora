package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfin"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfinconfig"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
)

type stateProcess struct {
	running   bool
	state     string
	forceStop bool
	stopErr   error
	started   time.Time
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
	return platform.ProcessInfo{PID: 42, PGID: 42, State: p.state}, true
}
func (p *stateProcess) Start(context.Context) error { p.running = true; return nil }
func (p *stateProcess) Stop(_ context.Context, force bool, _ time.Duration) error {
	p.forceStop = force
	if p.stopErr == nil {
		p.running = false
	}
	return p.stopErr
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
