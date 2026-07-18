package supervisor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfin"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

func TestControlOperationReportsStatePersistenceFailure(t *testing.T) {
	process := &stateProcess{running: true, started: time.Now()}
	s := persistentStateSupervisor(t.TempDir(), process)
	s.writeStateFile = func(string, []byte, os.FileMode) error { return errors.New("injected fsync failure") }
	reply := make(chan error, 1)
	s.handle(Request{Action: ActionStop, Reply: reply})
	err := <-reply
	if err == nil || !strings.Contains(err.Error(), "injected fsync failure") {
		t.Fatalf("operation persistence error = %v", err)
	}
	// A rejected operation is transactional: it must not execute later from
	// partially mutated in-memory state while the caller retries it.
	s.reconcile(context.Background())
	if !process.running || s.Status().DesiredState != model.DesiredRunning || s.Status().ManualStop {
		t.Fatalf("failed persistence changed lifecycle: running=%t status=%+v", process.running, s.Status())
	}
}

func TestSecondStateWriteFailureRestoresPreviousDurableIntent(t *testing.T) {
	directory := t.TempDir()
	process := &stateProcess{running: true, started: time.Now()}
	s := persistentStateSupervisor(directory, process)
	writes := 0
	s.writeStateFile = func(path string, data []byte, mode os.FileMode) error {
		writes++
		if writes == 2 {
			return errors.New("injected second-write failure")
		}
		return atomicWrite(path, data, mode)
	}
	reply := make(chan error, 1)
	s.handle(Request{Action: ActionStop, Reply: reply})
	if err := <-reply; err == nil || !strings.Contains(err.Error(), "second-write failure") {
		t.Fatalf("operation persistence error = %v", err)
	}
	replacement := persistentStateSupervisor(directory, process)
	if replacement.Status().ManualStop || replacement.Status().DesiredState != model.DesiredRunning {
		t.Fatalf("partial write replayed rejected stop: %+v", replacement.Status())
	}
}

func TestRestartBetweenAcceptedStopAndReconcilePreservesIntent(t *testing.T) {
	directory := t.TempDir()
	process := &stateProcess{running: true, started: time.Now()}
	first := persistentStateSupervisor(directory, process)
	reply := make(chan error, 1)
	first.handle(Request{Action: ActionStop, Reply: reply})
	if err := <-reply; err != nil {
		t.Fatal(err)
	}

	// Simulate Remora exiting after acknowledging the operation but before its
	// next reconcile. The replacement reads the durable intent and stops the
	// already-running Jellyfin generation instead of starting another one.
	replacement := persistentStateSupervisor(directory, process)
	replacement.reconcile(context.Background())
	replacement.reconcile(context.Background())
	if process.running || process.startCalls != 0 || process.stopCalls != 1 || replacement.Status().State != model.StateStopped {
		t.Fatalf("restart recovery running=%t starts=%d stops=%d state=%s", process.running, process.startCalls, process.stopCalls, replacement.Status().State)
	}
}

func TestRestartDuringReplacementConvergesToOneGeneration(t *testing.T) {
	directory := t.TempDir()
	process := &stateProcess{running: true, started: time.Now()}

	// This is the midpoint of a restart: the old generation has exited, but the
	// replacement has not yet started when Remora itself disappears.
	if err := process.Stop(context.Background(), false, time.Second); err != nil {
		t.Fatal(err)
	}
	replacement := persistentStateSupervisor(directory, process)
	replacement.nextStart = time.Time{}
	replacement.reconcile(context.Background())
	if !process.running || process.startCalls != 1 || process.duplicateStarts != 0 {
		t.Fatalf("replacement did not converge: running=%t starts=%d duplicates=%d", process.running, process.startCalls, process.duplicateStarts)
	}
	// A further reconcile may inspect health, but it must never create a second
	// child while the first replacement is alive.
	replacement.reconcile(context.Background())
	if process.startCalls != 1 || process.duplicateStarts != 0 {
		t.Fatalf("second generation was attempted: starts=%d duplicates=%d", process.startCalls, process.duplicateStarts)
	}
}

func TestStartFailureCircuitBoundsAutomaticRestartAttempts(t *testing.T) {
	process := &stateProcess{startErr: errors.New("injected exec failure")}
	s := stateSupervisor(t, process)
	for attempt := 0; attempt < 20; attempt++ {
		s.nextStart = time.Time{}
		s.reconcile(context.Background())
	}
	if process.startCalls != 5 || !s.processFailed || s.Status().State != model.StateProcessFailed {
		t.Fatalf("starts=%d failed=%t state=%s", process.startCalls, s.processFailed, s.Status().State)
	}
}

func persistentStateSupervisor(directory string, process *stateProcess) *Supervisor {
	cfg := &config.Config{
		Remora: config.RemoraConfig{
			ServerStartTimeout:  config.Duration{Duration: time.Minute},
			ServerStopTimeout:   config.Duration{Duration: time.Millisecond},
			IOTimeout:           config.Duration{Duration: time.Millisecond},
			RecoverySuccesses:   1,
			HeartbeatInterval:   config.Duration{Duration: time.Second},
			HealthAPIHeartbeat:  100,
			APIFailureThreshold: 3,
			DataDir:             directory,
		},
		Jellyfin: config.JellyfinConfig{DataDir: filepath.Join(directory, "jellyfin")},
	}
	return New(cfg, process, stateStorage{}, jellyfin.New("http://127.0.0.1:1", time.Millisecond), slog.New(slog.NewTextHandler(io.Discard, nil)))
}
