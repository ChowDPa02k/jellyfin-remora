package supervisor

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"testing/quick"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

// TestSupervisorStateMachineProperties drives the real reconcile function with
// generated command sequences. It checks safety invariants after every step,
// rather than asserting only a few hand-picked paths through the state machine.
func TestSupervisorStateMachineProperties(t *testing.T) {
	property := func(seed uint64) bool {
		process := &stateProcess{}
		supervisor := stateSupervisor(t, process)
		supervisor.writeStateFile = func(string, []byte, os.FileMode) error { return nil }
		random := rand.New(rand.NewSource(int64(seed)))
		unexpectedExits := 0

		for step := 0; step < 128; step++ {
			switch random.Intn(11) {
			case 0: // ordinary reconciliation
			case 1:
				submitPropertyAction(supervisor, ActionStart)
			case 2:
				submitPropertyAction(supervisor, ActionStop)
			case 3:
				submitPropertyAction(supervisor, ActionRestart)
			case 4: // fatal storage failure
				supervisor.status.Storage = []model.StorageResult{{Healthy: false, Fatal: true}}
			case 5: // storage recovery
				supervisor.status.Storage = []model.StorageResult{{Healthy: true}}
				supervisor.nextStart = time.Time{}
			case 6: // unexpected child exit
				if process.running {
					process.running = false
					unexpectedExits++
				}
			case 7: // durable database fence
				supervisor.databaseDamaged = true
				supervisor.status.Database.Damaged = true
			case 8: // administrator acknowledges fences and backoff
				submitPropertyAction(supervisor, ActionStart)
				supervisor.status.Storage = []model.StorageResult{{Healthy: true}}
				supervisor.nextStart = time.Time{}
			case 9: // advance a pending bounded retry
				supervisor.nextStart = time.Time{}
			case 10: // reject a random lifecycle action before persistence commits
				supervisor.writeStateFile = func(string, []byte, os.FileMode) error {
					return errors.New("injected property persistence failure")
				}
				submitPropertyAction(supervisor, []Action{ActionStart, ActionStop, ActionRestart}[random.Intn(3)])
				supervisor.writeStateFile = func(string, []byte, os.FileMode) error { return nil }
			}

			supervisor.reconcile(context.Background())
			if err := stateMachineInvariant(supervisor, process, unexpectedExits); err != nil {
				t.Logf("seed=%d step=%d error=%v status=%+v starts=%d stops=%d exits=%d", seed, step, err, supervisor.Status(), process.startCalls, process.stopCalls, unexpectedExits)
				return false
			}
		}
		return true
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func submitPropertyAction(s *Supervisor, action Action) {
	reply := make(chan error, 1)
	s.handle(Request{Action: action, Reply: reply})
	<-reply
}

func stateMachineInvariant(s *Supervisor, process *stateProcess, unexpectedExits int) error {
	status := s.Status()
	if process.duplicateStarts != 0 {
		return fmt.Errorf("duplicate Jellyfin start attempted %d times", process.duplicateStarts)
	}
	if process.startCalls > process.stopCalls+unexpectedExits+1 {
		return fmt.Errorf("more live generations started than terminated")
	}
	if status.PID != 0 && (!process.running || status.PID != process.PID()) {
		return fmt.Errorf("published PID does not identify the live Jellyfin process")
	}
	if status.ManualStop || status.DesiredState == model.DesiredStopped {
		if process.running || (status.State != model.StateStopped && status.State != model.StateStopping) {
			return fmt.Errorf("manual stop did not dominate: running=%t state=%s", process.running, status.State)
		}
	}
	for _, storage := range status.Storage {
		if storage.Fatal && process.running {
			return fmt.Errorf("fatal storage did not fence: running=%t state=%s", process.running, status.State)
		}
	}
	if s.databaseDamaged && process.running {
		return fmt.Errorf("database damage did not fence: running=%t state=%s", process.running, status.State)
	}
	s.mu.RLock()
	eventCount := len(s.events)
	s.mu.RUnlock()
	if eventCount > 256 {
		return fmt.Errorf("event history exceeded bound")
	}
	events := s.Events(256)
	for i := 1; i < len(events); i++ {
		if events[i].Sequence <= events[i-1].Sequence {
			return fmt.Errorf("event sequence is not strictly increasing")
		}
	}
	return nil
}
