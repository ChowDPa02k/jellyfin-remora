package supervisor

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
