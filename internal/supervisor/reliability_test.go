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
	"github.com/ChowDPa02K/jellyfin-remora/internal/contract"
	"github.com/ChowDPa02K/jellyfin-remora/internal/databasemonitor"
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

func TestFailedRollbackJournalPreventsRejectedIntentReplay(t *testing.T) {
	directory := t.TempDir()
	process := &stateProcess{running: true, started: time.Now()}
	s := persistentStateSupervisor(directory, process)
	realWriter := s.writeStateFile
	writes := 0
	s.writeStateFile = func(path string, data []byte, mode os.FileMode) error {
		writes++
		if writes >= 3 { // journal + runtime succeed; local and any restore fail
			return errors.New("injected persistent failure")
		}
		return realWriter(path, data, mode)
	}
	reply := make(chan error, 1)
	s.handle(Request{Action: ActionStop, Reply: reply})
	if err := <-reply; err == nil {
		t.Fatal("stop unexpectedly succeeded")
	}
	replacement := persistentStateSupervisor(directory, process)
	if replacement.Status().ManualStop || replacement.Status().DesiredState != model.DesiredRunning {
		t.Fatalf("rejected stop was replayed despite rollback journal: %+v", replacement.Status())
	}
	if replacement.rollbackState == nil {
		t.Fatal("replacement did not discover pending rollback recovery")
	}
}

func TestLifecycleIntentIsNotVisibleBeforePersistenceCommits(t *testing.T) {
	s := persistentStateSupervisor(t.TempDir(), &stateProcess{running: true, started: time.Now()})
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	writes := 0
	s.writeStateFile = func(string, []byte, os.FileMode) error {
		writes++
		if writes == 2 { // rollback journal is durable; proposed runtime write is blocked
			close(writeStarted)
			<-releaseWrite
		}
		return nil
	}
	reply := make(chan error, 1)
	go s.handle(Request{Action: ActionStop, Reply: reply})
	<-writeStarted
	if status := s.Status(); status.ManualStop || status.DesiredState != model.DesiredRunning {
		t.Fatalf("uncommitted lifecycle intent became visible: %+v", status)
	}
	close(releaseWrite)
	if err := <-reply; err != nil {
		t.Fatal(err)
	}
	if status := s.Status(); !status.ManualStop || status.DesiredState != model.DesiredStopped {
		t.Fatalf("committed lifecycle intent is not visible: %+v", status)
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

func TestManualStopPersistsWhenFatalStorageIsOnAnotherVolume(t *testing.T) {
	s := persistentStateSupervisor(t.TempDir(), &stateProcess{})
	s.status.ManualStop = true
	s.status.Storage = []model.StorageResult{{Target: "/Volumes/unavailable-media", Healthy: false, Fatal: true}}
	var paths []string
	s.writeStateFile = func(path string, _ []byte, _ os.FileMode) error {
		paths = append(paths, path)
		return nil
	}
	if err := s.persist(); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 || paths[2] != filepath.Join(s.cfg.Remora.DataDir, "jellyfin.state") {
		t.Fatalf("state writes=%v, want runtime, local, and healthy durable copies", paths)
	}
}

func TestFatalStatePathIsNotWritten(t *testing.T) {
	s := persistentStateSupervisor(t.TempDir(), &stateProcess{})
	s.status.ManualStop = true
	s.status.Storage = []model.StorageResult{{Target: filepath.Dir(s.cfg.Remora.DataDir), Healthy: false, Fatal: true}}
	writes := 0
	s.writeStateFile = func(string, []byte, os.FileMode) error { writes++; return nil }
	if err := s.persist(); err != nil {
		t.Fatal(err)
	}
	if writes != 2 {
		t.Fatalf("writes=%d, want runtime and local copies only", writes)
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

func TestDatabaseFenceRestoresAfterDurableStorageReturns(t *testing.T) {
	directory := t.TempDir()
	dataDir := filepath.Join(directory, "offline-share")
	s := persistentStateSupervisor(dataDir, &stateProcess{})
	s.localStatePath = filepath.Join(directory, "missing-local", contract.StateFileName)
	s.stateRestorePending = true
	s.status.Storage = []model.StorageResult{{Target: dataDir, Healthy: true}}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, contract.StateFileName), []byte("1\n0\n0\n1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.reconcile(context.Background())
	if !s.databaseDamaged || !s.Status().Database.Damaged || s.process.(*stateProcess).startCalls != 0 {
		t.Fatalf("database fence was not restored before start: damaged=%t status=%+v", s.databaseDamaged, s.Status())
	}
}

func TestPendingSafetyStateStopsAdoptedProcessWhileStorageIsDown(t *testing.T) {
	process := &stateProcess{running: true, started: time.Now()}
	s := persistentStateSupervisor(t.TempDir(), process)
	s.stateRestorePending = true
	s.status.Storage = []model.StorageResult{{Target: "/offline/share", Healthy: false, Fatal: true}}
	s.reconcile(context.Background())
	if process.running || process.stopCalls != 1 || s.Status().State != model.StateStorageFenced {
		t.Fatalf("pending safety state did not stop adopted process: running=%t stops=%d status=%+v", process.running, process.stopCalls, s.Status())
	}
}

func TestMalformedPersistentSafetyStateFailsClosed(t *testing.T) {
	for _, location := range []string{"local", "durable"} {
		for _, value := range []string{"0\n", "0\n0\nmanual\n0\n", "0\n9\n0\n0\n"} {
			t.Run(location+"_"+strings.ReplaceAll(value, "\n", "_"), func(t *testing.T) {
				directory := t.TempDir()
				process := &stateProcess{running: true, started: time.Now()}
				s := persistentStateSupervisor(directory, process)
				path := filepath.Join(s.cfg.Remora.DataDir, contract.StateFileName)
				if location == "local" {
					path = s.localStatePath
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
				s.stateRestorePending = true
				s.status.Storage = []model.StorageResult{{Target: directory, Healthy: true}}
				s.reconcile(context.Background())
				if process.running || s.Status().State != model.StateStorageFenced || !s.stateRestorePending {
					t.Fatalf("malformed state did not fail closed: running=%t pending=%t status=%+v", process.running, s.stateRestorePending, s.Status())
				}
			})
		}
	}
}

func TestStartDoesNotEraseDatabaseEvidenceArrivingDuringPersist(t *testing.T) {
	s := persistentStateSupervisor(t.TempDir(), &stateProcess{})
	detector := &databasemonitor.Detector{}
	s.SetDatabaseDamageSource(detector)
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	writes := 0
	s.writeStateFile = func(string, []byte, os.FileMode) error {
		writes++
		if writes == 1 {
			close(writeStarted)
			<-releaseWrite
		}
		return nil
	}
	reply := make(chan error, 1)
	go s.handle(Request{Action: ActionStart, Reply: reply})
	<-writeStarted
	_, _ = detector.Write([]byte("SQLite Error 11: database disk image is malformed\n"))
	close(releaseWrite)
	if err := <-reply; err != nil {
		t.Fatal(err)
	}
	if _, ok := detector.Candidate(time.Minute); !ok {
		t.Fatal("start erased corruption evidence observed during state persistence")
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
	s := newSupervisor(cfg, process, stateStorage{}, jellyfin.New("http://127.0.0.1:1", time.Millisecond), slog.New(slog.NewTextHandler(io.Discard, nil)), filepath.Join(directory, "local-state", contract.StateFileName))
	s.stateRestorePending = false
	return s
}
