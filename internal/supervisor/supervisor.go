package supervisor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/contract"
	"github.com/ChowDPa02K/jellyfin-remora/internal/databasemonitor"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfin"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfinconfig"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
)

type Action string

const (
	ActionStart       Action = "start"
	ActionStop        Action = "stop"
	ActionRestart     Action = "restart"
	ActionHealthcheck Action = "healthcheck"
)

type Request struct {
	Action Action
	Force  bool
	Reply  chan error
}

type StorageChecker interface {
	CheckDisk(context.Context, int) model.StorageResult
	CheckPaths(context.Context) []model.StorageResult
}

type ProcessManager interface {
	Executable() string
	Arguments() []string
	PID() int
	StartedAt() time.Time
	Adopt(context.Context) (bool, error)
	Info(context.Context) (platform.ProcessInfo, bool)
	Start(context.Context) error
	Stop(context.Context, bool, time.Duration) error
	WritePIDFile() error
	RemovePIDFile() error
}

type ConfigurationReconciler interface {
	Reconcile() (jellyfinconfig.Result, error)
}

type DatabaseDamageSource interface {
	Candidate(time.Duration) (databasemonitor.Evidence, bool)
	ResetBefore(time.Time)
}

type Supervisor struct {
	mu                   sync.RWMutex
	managementMu         sync.Mutex
	cfg                  *config.Config
	process              ProcessManager
	storage              StorageChecker
	client               *jellyfin.Client
	configuration        ConfigurationReconciler
	log                  *slog.Logger
	status               model.Status
	actions              chan Request
	forceStop            bool
	restartRequested     bool
	storageFenced        bool
	databaseDamaged      bool
	databaseFailures     int
	databaseSource       DatabaseDamageSource
	healthyStorageRuns   int
	apiFailures          int
	tick                 uint64
	wasRunning           bool
	processFailed        bool
	stopFailures         int
	nextStopRetry        time.Time
	crashes              []time.Time
	nextStart            time.Time
	hungSince            time.Time
	apiKey               string
	adminToken           string
	watchdogReady        bool
	watchdogFailed       bool
	wizardIncompleteRuns int
	initializationFails  int
	nextInitialization   time.Time
	sessionsInitialized  bool
	applicationReady     bool
	events               []model.Event
	eventSequence        uint64
	writeStateFile       func(string, []byte, os.FileMode) error
	removeStateFile      func(string) error
	localStatePath       string
	stateRestorePending  bool
	rollbackState        *persistedState
}

func New(cfg *config.Config, pm ProcessManager, sc StorageChecker, jc *jellyfin.Client, log *slog.Logger) *Supervisor {
	return newSupervisor(cfg, pm, sc, jc, log, localPersistentStatePath(cfg))
}

func newSupervisor(cfg *config.Config, pm ProcessManager, sc StorageChecker, jc *jellyfin.Client, log *slog.Logger, localStatePath string) *Supervisor {
	now := time.Now()
	s := &Supervisor{cfg: cfg, process: pm, storage: sc, client: jc, configuration: jellyfinconfig.New(cfg), log: log, actions: make(chan Request, 32), writeStateFile: atomicWrite, removeStateFile: os.Remove, localStatePath: localStatePath}
	if b, err := os.ReadFile(filepath.Join(cfg.Remora.DataDir, contract.APIKeyFileName)); err == nil {
		s.apiKey = strings.TrimSpace(string(b))
	}
	uid, username := runtimeIdentity(cfg.Jellyfin.RunAsUser)
	s.status = model.Status{State: model.StateInit, DesiredState: model.DesiredRunning, UID: uid, Username: username, Executable: pm.Executable(), Arguments: pm.Arguments(), LastTransition: now}
	s.recordEventLocked(model.Event{Timestamp: now, Type: "state_transition", State: model.StateInit})
	runtimeState := readPersistedState(filepath.Join(runtimeStateDir(cfg), contract.StateFileName))
	localState, _, localErr := readPersistedStateResult(s.localStatePath)
	durableState, _, durableErr := readPersistedStateResult(filepath.Join(cfg.Remora.DataDir, contract.StateFileName))
	s.stateRestorePending = localErr != nil || durableErr != nil
	if rollback, exists, err := readPersistedStateResult(s.rollbackJournalPath()); err == nil && exists {
		s.rollbackState = &rollback
		s.stateRestorePending = true
		runtimeState, localState, durableState = rollback, rollback, rollback
	}
	if runtimeState.ManualStop || localState.ManualStop || durableState.ManualStop {
		s.status.ManualStop = true
		s.status.DesiredState = model.DesiredStopped
	}
	if runtimeState.DatabaseDamaged || localState.DatabaseDamaged || durableState.DatabaseDamaged {
		s.databaseDamaged = true
		s.status.Database.Damaged = true
	}
	return s
}

func (s *Supervisor) SetDatabaseDamageSource(source DatabaseDamageSource) {
	s.databaseSource = source
}

func (s *Supervisor) Run(ctx context.Context) error {
	s.transition(model.StatePreflight, "")
	adopted, err := s.process.Adopt(ctx)
	if err != nil {
		s.setError("process adoption: " + err.Error())
	} else if adopted {
		s.log.Info("adopted existing Jellyfin process", "pid", s.process.PID())
		_ = s.process.WritePIDFile()
	}
	s.setMountRecoveryAllowed(!adopted)
	s.runStorageChecks(ctx, true)
	s.reconcile(ctx)
	ticker := time.NewTicker(s.cfg.Remora.HeartbeatInterval.Duration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.statusCopyUpdate(func(st *model.Status) { st.DesiredState = model.DesiredStopped })
			// A prior remoractl stop has already removed the managed PID and
			// completed the lifecycle transition. Exiting Remora must not emit a
			// second STOPPING event or probe Jellyfin's shutdown API again.
			if s.process.PID() > 0 {
				stopCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Remora.ServerStopTimeout.Duration+5*time.Second)
				_ = s.stop(stopCtx, false)
				cancel()
			}
			return nil
		case req := <-s.actions:
			s.handle(req)
		case <-ticker.C:
			s.tick++
			s.runStorageChecks(ctx, false)
			s.reconcile(ctx)
		}
	}
}

func (s *Supervisor) Submit(ctx context.Context, action Action, force bool) error {
	reply := make(chan error, 1)
	select {
	case s.actions <- Request{Action: action, Force: force, Reply: reply}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Supervisor) handle(req Request) {
	var err error
	actionStarted := time.Now()
	var beforeData []byte
	type lifecycleSnapshot struct {
		status              model.Status
		manualStop          bool
		desired             model.DesiredState
		forceStop           bool
		restartRequested    bool
		processFailed       bool
		stopFailures        int
		nextStopRetry       time.Time
		crashes             []time.Time
		nextStart           time.Time
		initializationFails int
		nextInitialization  time.Time
		databaseDamaged     bool
		databaseFailures    int
		database            model.DatabaseResult
	}
	snapshot := func() lifecycleSnapshot {
		return lifecycleSnapshot{
			status: s.status, manualStop: s.status.ManualStop, desired: s.status.DesiredState,
			forceStop: s.forceStop, restartRequested: s.restartRequested,
			processFailed: s.processFailed, stopFailures: s.stopFailures, nextStopRetry: s.nextStopRetry,
			crashes: append([]time.Time(nil), s.crashes...), nextStart: s.nextStart,
			initializationFails: s.initializationFails, nextInitialization: s.nextInitialization,
			databaseDamaged: s.databaseDamaged, databaseFailures: s.databaseFailures,
			database: s.status.Database,
		}
	}
	apply := func(state lifecycleSnapshot) {
		s.status = state.status
		s.status.ManualStop, s.status.DesiredState = state.manualStop, state.desired
		s.forceStop, s.restartRequested = state.forceStop, state.restartRequested
		s.processFailed, s.stopFailures, s.nextStopRetry = state.processFailed, state.stopFailures, state.nextStopRetry
		s.crashes, s.nextStart = append([]time.Time(nil), state.crashes...), state.nextStart
		s.initializationFails, s.nextInitialization = state.initializationFails, state.nextInitialization
		s.databaseDamaged, s.databaseFailures, s.status.Database = state.databaseDamaged, state.databaseFailures, state.database
	}
	lifecycle := req.Action == ActionStart || req.Action == ActionStop || req.Action == ActionRestart
	var before, proposed lifecycleSnapshot
	s.mu.Lock()
	if lifecycle {
		beforeData, _ = encodeState(s.status, s.databaseDamaged)
		before = snapshot()
	}
	switch req.Action {
	case ActionStart:
		s.status.ManualStop = false
		s.status.DesiredState = model.DesiredRunning
		s.processFailed = false
		s.stopFailures = 0
		s.nextStopRetry = time.Time{}
		s.crashes = nil
		s.nextStart = time.Time{}
		s.initializationFails = 0
		s.nextInitialization = time.Time{}
		s.databaseDamaged = false
		s.databaseFailures = 0
		s.status.Database = model.DatabaseResult{}
	case ActionStop:
		s.status.ManualStop = true
		s.status.DesiredState = model.DesiredStopped
		s.forceStop = req.Force
	case ActionRestart:
		s.status.ManualStop = false
		s.status.DesiredState = model.DesiredRunning
		s.restartRequested = true
		s.forceStop = req.Force
		s.processFailed = false
		s.crashes = nil
		s.initializationFails = 0
		s.nextInitialization = time.Time{}
	case ActionHealthcheck:
	default:
		err = fmt.Errorf("unknown action %q", req.Action)
	}
	if lifecycle {
		proposed = snapshot()
		apply(before)
	}
	s.mu.Unlock()
	if req.Action == ActionHealthcheck {
		s.immediateHealthcheck()
	}
	if err == nil && lifecycle {
		writer := s.writeStateFile
		if writer == nil {
			writer = atomicWrite
		}
		if journalErr := writer(s.rollbackJournalPath(), beforeData, 0640); journalErr != nil {
			err = fmt.Errorf("create %s rollback journal: %w", req.Action, journalErr)
		}
	}
	if err == nil {
		var persistErr error
		if lifecycle {
			persistErr = s.persistState(proposed.status, proposed.databaseDamaged)
		} else {
			persistErr = s.persist()
		}
		if persistErr != nil {
			err = fmt.Errorf("persist %s operation: %w", req.Action, persistErr)
		} else if lifecycle {
			remover := s.removeStateFile
			if remover == nil {
				remover = os.Remove
			}
			if removeErr := remover(s.rollbackJournalPath()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = fmt.Errorf("commit %s operation: remove rollback journal: %w", req.Action, removeErr)
			}
		}
	}
	if err == nil && lifecycle {
		s.mu.Lock()
		apply(proposed)
		s.mu.Unlock()
	}
	if err == nil && req.Action == ActionStart && s.databaseSource != nil {
		s.databaseSource.ResetBefore(actionStarted)
	}
	req.Reply <- err
}

func (s *Supervisor) immediateHealthcheck() {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Remora.IOTimeout.Duration*time.Duration(max(1, len(s.cfg.Disks))))
	defer cancel()
	s.runStorageChecks(ctx, true)
	h := s.client.Health(ctx)
	s.mu.Lock()
	s.status.Jellyfin = h
	s.mu.Unlock()
	s.persistBestEffort()
}

func (s *Supervisor) runStorageChecks(ctx context.Context, all bool) {
	s.mu.RLock()
	old := append([]model.StorageResult(nil), s.status.Storage...)
	s.mu.RUnlock()
	if len(old) < len(s.cfg.Disks) {
		old = make([]model.StorageResult, len(s.cfg.Disks))
		all = true
	}
	old = old[:len(s.cfg.Disks)]
	for i, d := range s.cfg.Disks {
		if all || s.tick%uint64(max(1, d.Heartbeat)) == 0 {
			old[i] = s.storage.CheckDisk(ctx, i)
		}
	}
	fatal := false
	for _, r := range old {
		if r.Fatal {
			fatal = true
			break
		}
	}
	if !fatal {
		old = append(old, s.storage.CheckPaths(ctx)...)
	}
	s.mu.Lock()
	s.status.Storage = old
	s.mu.Unlock()
}

func (s *Supervisor) reconcile(ctx context.Context) {
	s.mu.RLock()
	desired := s.status.DesiredState
	manual := s.status.ManualStop
	storageResults := append([]model.StorageResult(nil), s.status.Storage...)
	force := s.forceStop
	restart := s.restartRequested
	s.mu.RUnlock()
	fatal := false
	degraded := false
	for _, r := range storageResults {
		if r.Fatal {
			fatal = true
		}
		if !r.Healthy {
			degraded = true
		}
	}
	pi, running := s.process.Info(ctx)
	if running {
		s.mu.Lock()
		s.status.PID = pi.PID
		s.status.CPUPercent = pi.CPUPercent
		s.status.MemoryBytes = pi.MemoryBytes
		s.status.FFmpegProcesses = pi.FFmpegProcesses
		s.status.ProcessState = pi.State
		s.status.Ports = append(s.status.Ports[:0], pi.Ports...)
		s.mu.Unlock()
	}
	if s.stateRestorePending {
		if degraded {
			s.storageFenced = true
			if !s.stopWhileStateUnavailable(ctx, running, "persistent safety state is unavailable until storage recovery") {
				return
			}
			s.transition(model.StateStorageFenced, "persistent safety state is unavailable until storage recovery")
			return
		}
		if err := s.reloadPersistentSafetyState(); err != nil {
			s.storageFenced = true
			if !s.stopWhileStateUnavailable(ctx, running, "cannot restore persistent safety state: "+err.Error()) {
				return
			}
			s.transition(model.StateStorageFenced, "cannot restore persistent safety state: "+err.Error())
			return
		}
	}
	exitedGeneration := s.wasRunning && !running
	if exitedGeneration && !manual && desired != model.DesiredStopped {
		s.recordCrash()
		s.scheduleRestart()
	}
	if !running {
		s.setMountRecoveryAllowed(true)
		s.hungSince = time.Time{}
		s.applicationReady = false
		s.mu.Lock()
		s.status.PID = 0
		s.status.ProcessState = ""
		s.status.CPUPercent = 0
		s.status.MemoryBytes = 0
		s.status.FFmpegProcesses = 0
		s.status.Ports = nil
		s.status.ActiveTranscodes = 0
		s.status.Sessions = nil
		s.mu.Unlock()
		s.sessionsInitialized = false
		_ = s.process.RemovePIDFile()
	}
	s.wasRunning = running
	if manual || desired == model.DesiredStopped {
		if running {
			if time.Now().Before(s.nextStopRetry) {
				s.transition(model.StateProcessFailed, "manual stop is waiting for bounded retry backoff")
				s.persistBestEffort()
				return
			}
			if err := s.stop(ctx, force); err != nil {
				s.recordStopFailure("manual stop", err)
				s.persistBestEffort()
				return
			}
			s.stopFailures = 0
			s.nextStopRetry = time.Time{}
		} else {
			s.stopFailures = 0
			s.nextStopRetry = time.Time{}
			s.transition(model.StateStopped, "")
		}
		s.setMountRecoveryAllowed(true)
		s.clearOneShots()
		s.persistBestEffort()
		return
	}
	if fatal {
		s.storageFenced = true
		s.healthyStorageRuns = 0
		if running {
			if time.Now().Before(s.nextStopRetry) {
				s.transition(model.StateProcessFailed, "storage fence stop is waiting for bounded retry backoff")
				s.persistBestEffort()
				return
			}
			if err := s.stop(ctx, false); err != nil {
				s.recordStopFailure("storage fence stop", err)
				s.persistBestEffort()
				return
			}
			s.stopFailures = 0
			s.nextStopRetry = time.Time{}
		}
		s.setMountRecoveryAllowed(true)
		s.transition(model.StateStorageFenced, "required storage is unhealthy")
		s.clearOneShots()
		s.persistBestEffort()
		return
	}
	if s.storageFenced {
		if degraded {
			s.healthyStorageRuns = 0
			s.transition(model.StateStorageFenced, "storage recovery requires every check to be healthy")
			s.persistBestEffort()
			return
		}
		s.healthyStorageRuns++
		if s.healthyStorageRuns < s.cfg.Remora.RecoverySuccesses {
			s.transition(model.StateStorageFenced, fmt.Sprintf("storage recovery confirmation %d/%d", s.healthyStorageRuns, s.cfg.Remora.RecoverySuccesses))
			s.persistBestEffort()
			return
		}
		s.storageFenced = false
		s.healthyStorageRuns = 0
	}
	if exitedGeneration && s.evaluateExitedGenerationDatabaseDamage(ctx) {
		s.transition(model.StateDatabaseDamaged, "confirmed Jellyfin database damage after process exit; automatic restart is fenced until remoractl start")
		s.clearOneShots()
		s.persistBestEffort()
		return
	}
	if s.processFailed {
		if s.Status().State == model.StateProcessFailed {
			s.transition(model.StateProcessFailed, "")
		} else {
			s.transition(model.StateProcessFailed, "restart rate limit exceeded")
		}
		s.persistBestEffort()
		return
	}
	if s.databaseDamaged {
		if running {
			if err := s.stop(ctx, false); err != nil {
				s.transition(model.StateDatabaseDamaged, "database damage is latched and Jellyfin could not be stopped: "+err.Error())
				s.persistBestEffort()
				return
			}
		}
		s.transition(model.StateDatabaseDamaged, "Jellyfin database damage is latched; repair or restore the database, then use remoractl start")
		s.clearOneShots()
		s.persistBestEffort()
		return
	}
	uninterruptible := running && (strings.Contains(pi.State, "D") || strings.Contains(pi.State, "U"))
	if uninterruptible {
		if s.hungSince.IsZero() {
			s.hungSince = time.Now()
			s.log.Debug("Jellyfin entered an uninterruptible process state", "process_state", pi.State)
		}
		if time.Since(s.hungSince) >= s.cfg.Remora.ServerStopTimeout.Duration {
			if err := s.stop(ctx, true); err != nil {
				s.processFailed = true
				s.transition(model.StateProcessFailed, "hung Jellyfin process could not be killed")
				s.persistBestEffort()
				return
			}
			s.recordCrash()
			s.scheduleRestart()
			s.transition(model.StateRestartBackoff, "Jellyfin remained in an uninterruptible process state")
			s.persistBestEffort()
			return
		}
		// A transient Darwin U state is common while Jellyfin scans a large
		// library. Keep evaluating storage and /health; those debounced signals
		// distinguish legitimate I/O waits from an actually unavailable server.
	} else {
		if !s.hungSince.IsZero() {
			s.log.Debug("Jellyfin left the uninterruptible process state")
		}
		s.hungSince = time.Time{}
	}
	if restart && running {
		if err := s.stop(ctx, force); err != nil {
			s.processFailed = true
			s.transition(model.StateProcessFailed, "Jellyfin could not be stopped: "+err.Error())
			s.persistBestEffort()
			return
		}
		running = false
		s.scheduleRestart()
	}
	if !running {
		if time.Now().Before(s.nextStart) {
			s.transition(model.StateRestartBackoff, "")
			s.clearOneShots()
			s.persistBestEffort()
			return
		}
		configurationResult, configurationErr := s.configuration.Reconcile()
		if configurationErr != nil {
			s.processFailed = true
			s.transition(model.StateProcessFailed, "Jellyfin configuration reconciliation failed: "+configurationErr.Error())
			s.clearOneShots()
			s.persistBestEffort()
			return
		}
		if len(configurationResult.ChangedFiles) > 0 {
			s.log.Info("reconciled Jellyfin XML configuration", "files", configurationResult.ChangedFiles, "backups", configurationResult.BackupFiles)
		}
		if err := s.process.Start(ctx); err != nil {
			s.setError(err.Error())
			s.recordCrash()
			s.scheduleRestart()
			s.transition(model.StateRestartBackoff, err.Error())
		} else {
			s.wasRunning = true
			s.setMountRecoveryAllowed(false)
			s.apiFailures = 0
			s.applicationReady = false
			s.mu.Lock()
			s.status.Jellyfin = model.HealthResult{}
			s.status.Sessions = nil
			s.mu.Unlock()
			s.sessionsInitialized = false
			s.transition(model.StateStarting, "")
			if err := s.process.WritePIDFile(); err != nil {
				s.log.Warn("cannot write PID file", "error", err)
			}
		}
		s.clearOneShots()
		s.persistBestEffort()
		return
	}
	age := time.Since(s.process.StartedAt())
	// Jellyfin 12 exposes a lightweight setup listener before its core and
	// database are ready. During an ordinary restart that listener can briefly
	// report StartupWizardCompleted=false for an already initialized server.
	// Only trust setup state after /health proves the full application is ready.
	s.mu.RLock()
	previousHealthCheck := s.status.Jellyfin.CheckedAt
	s.mu.RUnlock()
	readiness := s.client.Health(ctx)
	var info jellyfin.PublicInfo
	var infoErr error
	if readiness.Healthy {
		info, infoErr = s.client.PublicInfo(ctx)
	} else {
		infoErr = errors.New("Jellyfin core is not ready")
	}
	if readiness.Healthy && infoErr == nil {
		s.applicationReady = true
	}
	applicationHealth := readiness
	if readiness.Healthy && infoErr != nil && !s.applicationReady {
		applicationHealth.Healthy = false
		applicationHealth.Error = "Jellyfin public information is unavailable: " + infoErr.Error()
	} else if readiness.Healthy && infoErr != nil {
		s.log.Debug("cannot refresh Jellyfin public information; retaining established application readiness", "error", infoErr)
	}
	s.mu.Lock()
	s.status.Jellyfin = applicationHealth
	s.mu.Unlock()
	if infoErr == nil && info.StartupWizardCompleted != nil && !*info.StartupWizardCompleted {
		s.wizardIncompleteRuns++
		if age < 10*time.Second || s.wizardIncompleteRuns < 3 {
			s.transition(model.StateStarting, "confirming incomplete Jellyfin startup wizard")
			s.persistBestEffort()
			return
		}
		s.transition(model.StateFirstStart, "")
		if time.Now().Before(s.nextInitialization) {
			s.transition(model.StateFirstStart, "waiting for Jellyfin first-start initialization retry")
			s.persistBestEffort()
			return
		}
		if err := s.initializeServer(ctx); err != nil {
			s.initializationFails++
			message := "Jellyfin first-start initialization: " + err.Error()
			if s.initializationFails >= 5 {
				s.processFailed = true
				if stopErr := s.stop(ctx, false); stopErr != nil {
					message += "; incomplete Jellyfin could not be stopped: " + stopErr.Error()
				}
				s.transition(model.StateProcessFailed, message+"; retry limit exceeded; use remoractl start to reset")
			} else {
				delay := retryDelay(s.initializationFails)
				s.nextInitialization = time.Now().Add(delay)
				s.transition(model.StateFirstStart, fmt.Sprintf("%s; retrying in %s", message, delay))
			}
			s.persistBestEffort()
			return
		}
		s.initializationFails = 0
		s.nextInitialization = time.Time{}
		if err := s.stop(ctx, false); err != nil {
			s.processFailed = true
			s.transition(model.StateProcessFailed, "initialized Jellyfin could not be stopped: "+err.Error())
			s.persistBestEffort()
			return
		}
		s.scheduleRestart()
		s.transition(model.StateRestartBackoff, "Jellyfin initialization completed; restarting")
		s.persistBestEffort()
		return
	}
	if infoErr != nil || info.StartupWizardCompleted == nil || *info.StartupWizardCompleted {
		s.wizardIncompleteRuns = 0
	}
	if infoErr == nil && info.StartupWizardCompleted != nil && *info.StartupWizardCompleted {
		s.initializationFails = 0
		s.nextInitialization = time.Time{}
	}
	if infoErr == nil {
		s.mu.Lock()
		s.status.Version = info.Version
		s.status.ServerName = info.ServerName
		s.mu.Unlock()
	}
	if infoErr == nil && s.currentAPIKey() == "" {
		if err := s.ensureAPIKey(ctx); err != nil {
			s.log.Warn("cannot provision Remora API key", "error", err)
		}
	}
	if s.evaluateDatabaseDamage(ctx, readiness) {
		if err := s.stop(ctx, false); err != nil {
			s.transition(model.StateDatabaseDamaged, "confirmed Jellyfin database damage; process stop failed: "+err.Error())
		} else {
			s.transition(model.StateDatabaseDamaged, "confirmed Jellyfin database damage; automatic restart is fenced until remoractl start")
		}
		s.clearOneShots()
		s.persistBestEffort()
		return
	}
	if infoErr == nil && s.cfg.Remora.UserLoginWatchdog.Enabled && !s.watchdogReady {
		if err := s.ensureWatchdog(ctx); err != nil {
			s.watchdogFailed = true
			s.setError("login watchdog setup: " + err.Error())
		} else {
			s.watchdogFailed = false
		}
	}
	credentialHeartbeat := max(1, s.cfg.Remora.UserLoginWatchdog.Heartbeat)
	if !s.cfg.Remora.UserLoginWatchdog.Enabled {
		credentialHeartbeat = max(1, s.cfg.Remora.HealthAPIHeartbeat)
	}
	if apiKey := s.currentAPIKey(); infoErr == nil && apiKey != "" && s.tick%uint64(credentialHeartbeat) == 0 {
		if err := s.client.ValidateAPIKey(ctx, apiKey); err != nil {
			var apiErr *jellyfin.APIError
			if errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403) {
				s.setCredentials("", "")
				s.watchdogReady = false
				if recoverErr := s.ensureAPIKey(ctx); recoverErr != nil {
					s.transition(model.StateDegraded, "Remora API key recovery failed: "+recoverErr.Error())
					s.persistBestEffort()
					return
				}
			} else {
				s.transition(model.StateDegraded, "Remora API key validation failed: "+err.Error())
				s.persistBestEffort()
				return
			}
		}
	}
	if infoErr == nil && s.watchdogReady && s.tick%uint64(max(1, s.cfg.Remora.UserLoginWatchdog.Heartbeat)) == 0 {
		if err := s.client.EnsureWatchdogUser(ctx, s.adminCredential(), s.cfg.Remora.UserLoginWatchdog); err != nil {
			s.watchdogFailed = true
			s.transition(model.StateDegraded, "login watchdog failed: "+err.Error())
			s.persistBestEffort()
			return
		}
		s.watchdogFailed = false
	}
	if apiKey := s.currentAPIKey(); infoErr == nil && apiKey != "" && (!s.sessionsInitialized || s.tick%uint64(max(1, s.cfg.Remora.HealthAPIHeartbeat)) == 0) {
		sessions, err := s.client.Sessions(ctx, apiKey)
		if err != nil {
			s.log.Debug("cannot refresh Jellyfin sessions", "error", err)
			s.mu.Lock()
			s.status.Sessions = nil
			s.mu.Unlock()
			s.sessionsInitialized = false
		} else {
			s.mu.Lock()
			s.status.Sessions = sessions
			s.mu.Unlock()
			s.sessionsInitialized = true
		}
	}
	healthCountDue := s.tick%uint64(max(1, s.cfg.Remora.HealthAPIHeartbeat)) == 0 ||
		(age >= s.cfg.Remora.ServerStartTimeout.Duration && previousHealthCheck.IsZero())
	if healthCountDue {
		if readiness.Healthy {
			s.apiFailures = 0
		} else {
			s.apiFailures++
		}
	}
	health := applicationHealth
	if health.Healthy {
		if s.watchdogFailed {
			s.transition(model.StateDegraded, "login watchdog remains unhealthy")
		} else if s.Status().Database.Suspected {
			s.transition(model.StateDegraded, "Jellyfin database health is suspect; awaiting corruption evidence and API confirmation")
		} else if degraded {
			s.transition(model.StateDegraded, "non-fatal storage degradation")
		} else {
			s.transition(model.StateRunning, "")
		}
		s.crashes = nil
	} else if readiness.Healthy {
		s.transition(model.StateStarting, health.Error)
	} else if !s.applicationReady && age < s.cfg.Remora.ServerStartTimeout.Duration {
		s.transition(model.StateStarting, health.Error)
	} else if s.apiFailures >= s.cfg.Remora.APIFailureThreshold {
		if err := s.stop(ctx, false); err != nil {
			s.processFailed = true
			s.transition(model.StateProcessFailed, "unhealthy Jellyfin could not be stopped: "+err.Error())
			s.persistBestEffort()
			return
		}
		s.recordCrash()
		s.scheduleRestart()
		s.transition(model.StateRestartBackoff, "Jellyfin health check failed")
	} else {
		s.transition(model.StateDegraded, health.Error)
	}
	s.clearOneShots()
	s.persistBestEffort()
}

func (s *Supervisor) evaluateExitedGenerationDatabaseDamage(ctx context.Context) bool {
	if !s.cfg.Remora.Monitoring.Database.IsEnabled() || s.databaseSource == nil {
		return false
	}
	if _, suspected := s.databaseSource.Candidate(s.cfg.Remora.Monitoring.Database.ConfirmationWindow.Duration); !suspected {
		return false
	}
	// A valid console signature plus the emitting process becoming unavailable
	// are the two independent signals. Do not start another generation merely
	// to accumulate ordinary heartbeat failures against the same evidence.
	s.databaseFailures = max(s.databaseFailures, s.cfg.Remora.Monitoring.Database.FailureThreshold-1)
	return s.evaluateDatabaseDamage(ctx, model.HealthResult{Healthy: false, Error: "managed Jellyfin process exited"})
}

func (s *Supervisor) stopWhileStateUnavailable(ctx context.Context, running bool, reason string) bool {
	if !running {
		return true
	}
	if time.Now().Before(s.nextStopRetry) {
		s.transition(model.StateStorageFenced, reason+"; stop retry is in bounded backoff")
		return false
	}
	if err := s.stop(ctx, false); err != nil {
		s.recordStopFailure("persistent-state fence stop", err)
		s.transition(model.StateStorageFenced, reason+"; Jellyfin could not be stopped: "+err.Error())
		return false
	}
	s.stopFailures = 0
	s.nextStopRetry = time.Time{}
	return true
}

func (s *Supervisor) recordStopFailure(operation string, err error) {
	s.stopFailures++
	s.processFailed = true
	delay := retryDelay(s.stopFailures)
	s.nextStopRetry = time.Now().Add(delay)
	s.transition(model.StateProcessFailed, fmt.Sprintf("%s failed (attempt %d); retrying in %s: %v", operation, s.stopFailures, delay, err))
}

func (s *Supervisor) evaluateDatabaseDamage(ctx context.Context, readiness model.HealthResult) bool {
	if !s.cfg.Remora.Monitoring.Database.IsEnabled() || s.databaseSource == nil {
		return false
	}
	probeMessage := ""
	apiFailed := false
	probeDue := s.tick%uint64(max(1, s.cfg.Remora.HealthAPIHeartbeat)) == 0
	if token := s.currentAPIKey(); probeDue && token != "" {
		if err := s.client.ProbeDatabase(ctx, token); err != nil {
			var apiErr *jellyfin.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode >= 500 {
				apiFailed = true
				probeMessage = "Jellyfin database-backed API probe failed: " + err.Error()
			}
		}
	}
	evidence, suspected := s.databaseSource.Candidate(s.cfg.Remora.Monitoring.Database.ConfirmationWindow.Duration)
	if probeDue {
		if suspected && (!readiness.Healthy || apiFailed) {
			s.databaseFailures++
		} else if !apiFailed {
			s.databaseFailures = 0
		} else {
			// Retain API-only failures as suspicion, but never promote them to
			// damage until Jellyfin itself emits a corruption signature.
			s.databaseFailures++
		}
	}
	if !suspected && s.databaseFailures == 0 {
		s.statusCopyUpdate(func(st *model.Status) {
			if !st.Database.Damaged {
				st.Database = model.DatabaseResult{}
			}
		})
		return false
	}
	s.statusCopyUpdate(func(st *model.Status) {
		st.Database.Suspected = true
		if suspected {
			st.Database.Message = evidence.Message
			st.Database.DetectedAt = evidence.DetectedAt
		} else if probeMessage != "" {
			st.Database.Message = probeMessage
			st.Database.DetectedAt = time.Now()
		}
	})
	if !suspected {
		return false
	}
	if s.databaseFailures < s.cfg.Remora.Monitoring.Database.FailureThreshold {
		return false
	}
	s.databaseDamaged = true
	s.statusCopyUpdate(func(st *model.Status) {
		st.Database.Damaged = true
		st.Database.Suspected = false
	})
	s.log.Error("Jellyfin database damage confirmed", "evidence", evidence.Message, "detected_at", evidence.DetectedAt)
	return true
}

func (s *Supervisor) setMountRecoveryAllowed(allowed bool) {
	if controller, ok := s.storage.(interface{ SetMountRecoveryAllowed(bool) }); ok {
		controller.SetMountRecoveryAllowed(allowed)
	}
}

func (s *Supervisor) initializeServer(ctx context.Context) error {
	bootstrapUser, err := s.client.CompleteStartup(ctx, s.cfg.Init)
	if err != nil {
		return err
	}
	auth, err := s.client.Authenticate(ctx, bootstrapUser, s.cfg.Init.Password)
	if err != nil {
		return err
	}
	if bootstrapUser != s.cfg.Init.User {
		if err := s.client.UpdateUsername(ctx, auth.AccessToken, auth.User, s.cfg.Init.User); err != nil {
			return err
		}
		auth, err = s.client.Authenticate(ctx, s.cfg.Init.User, s.cfg.Init.Password)
		if err != nil {
			return err
		}
	}
	if err := s.client.UpdateServerName(ctx, auth.AccessToken, s.cfg.Init.ServerName); err != nil {
		return err
	}
	s.setAdminToken(auth.AccessToken)
	if err := s.ensureAPIKey(ctx); err != nil {
		return err
	}
	return s.ensureWatchdog(ctx)
}
func (s *Supervisor) ensureAPIKey(ctx context.Context) error {
	credential := s.adminCredential()
	if credential == "" {
		auth, err := s.client.Authenticate(ctx, s.cfg.Init.User, s.cfg.Init.Password)
		if err != nil {
			return err
		}
		s.setAdminToken(auth.AccessToken)
		credential = auth.AccessToken
	}
	key, err := s.client.EnsureAPIKey(ctx, credential)
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.cfg.Remora.DataDir, contract.APIKeyFileName), []byte(key+"\n"), 0600); err != nil {
		return err
	}
	s.setAPIKey(key)
	return nil
}
func (s *Supervisor) ensureWatchdog(ctx context.Context) error {
	if !s.cfg.Remora.UserLoginWatchdog.Enabled {
		return nil
	}
	if err := s.client.EnsureWatchdogUser(ctx, s.adminCredential(), s.cfg.Remora.UserLoginWatchdog); err != nil {
		return err
	}
	s.watchdogReady = true
	return nil
}
func (s *Supervisor) adminCredential() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.adminToken != "" {
		return s.adminToken
	}
	// A Remora API key can authorize administrative API calls, but watchdog
	// login and /Users/Me always use the watchdog user's persistent session.
	return s.apiKey
}

func (s *Supervisor) currentAPIKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.apiKey
}

func (s *Supervisor) setAPIKey(key string) {
	s.mu.Lock()
	s.apiKey = key
	s.mu.Unlock()
}

func (s *Supervisor) setAdminToken(token string) {
	s.mu.Lock()
	s.adminToken = token
	s.mu.Unlock()
}

func (s *Supervisor) setCredentials(apiKey, adminToken string) {
	s.mu.Lock()
	s.apiKey = apiKey
	s.adminToken = adminToken
	s.mu.Unlock()
}

func (s *Supervisor) APIKeys(ctx context.Context) ([]model.APIKey, error) {
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	keys, err := s.client.APIKeys(ctx, s.adminCredential())
	if err != nil {
		return nil, err
	}
	return publicAPIKeys(keys, s.currentAPIKey()), nil
}

func (s *Supervisor) CreateAPIKey(ctx context.Context, name string) (model.APIKey, error) {
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return model.APIKey{}, errors.New("API key name must contain 1 to 64 characters")
	}
	if name == "Jellyfin Remora" {
		return model.APIKey{}, errors.New("API key name Jellyfin Remora is reserved for the supervisor")
	}
	credential := s.adminCredential()
	before, err := s.client.APIKeys(ctx, credential)
	if err != nil {
		return model.APIKey{}, err
	}
	known := make(map[string]bool, len(before))
	for _, key := range before {
		known[key.AccessToken] = true
	}
	if err := s.client.CreateAPIKey(ctx, credential, name); err != nil {
		return model.APIKey{}, err
	}
	after, err := s.client.APIKeys(ctx, credential)
	if err != nil {
		return model.APIKey{}, err
	}
	for _, key := range after {
		if key.AppName == name && !known[key.AccessToken] {
			created := publicAPIKey(key, s.currentAPIKey())
			s.recordEvent(model.Event{Type: "api_key_created", Message: name})
			return created, nil
		}
	}
	return model.APIKey{}, errors.New("Jellyfin created the API key but did not return it")
}

func (s *Supervisor) DeleteAPIKey(ctx context.Context, id string) error {
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	if len(id) < 8 {
		return errors.New("API key ID must contain at least 8 hexadecimal characters")
	}
	credential := s.adminCredential()
	keys, err := s.client.APIKeys(ctx, credential)
	if err != nil {
		return err
	}
	var matched *jellyfin.AuthenticationInfo
	for i := range keys {
		if strings.HasPrefix(strings.ToLower(apiKeyID(keys[i].AccessToken)), strings.ToLower(id)) {
			if matched != nil {
				return errors.New("API key ID is ambiguous")
			}
			matched = &keys[i]
		}
	}
	if matched == nil {
		return errors.New("API key was not found")
	}
	if matched.AccessToken == s.currentAPIKey() {
		return errors.New("refusing to revoke the supervisor's active API key")
	}
	if err := s.client.RevokeAPIKey(ctx, credential, matched.AccessToken); err != nil {
		return err
	}
	s.recordEvent(model.Event{Type: "api_key_deleted", Message: matched.AppName})
	return nil
}

func (s *Supervisor) Sessions(ctx context.Context) ([]model.Session, error) {
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	return s.client.Sessions(ctx, s.currentAPIKey())
}

func (s *Supervisor) StopSession(ctx context.Context, id string) error {
	s.managementMu.Lock()
	defer s.managementMu.Unlock()
	if len(id) < 8 {
		return errors.New("session ID must contain at least 8 characters")
	}
	credential := s.currentAPIKey()
	sessions, err := s.client.Sessions(ctx, credential)
	if err != nil {
		return err
	}
	var matched *model.Session
	for i := range sessions {
		if strings.HasPrefix(strings.ToLower(sessions[i].ID), strings.ToLower(id)) {
			if matched != nil {
				return errors.New("session ID is ambiguous")
			}
			matched = &sessions[i]
		}
	}
	if matched == nil {
		return errors.New("session was not found")
	}
	if err := s.client.StopSession(ctx, credential, matched.ID); err != nil {
		return err
	}
	s.recordEvent(model.Event{Type: "session_stopped", Message: matched.User})
	return nil
}

func publicAPIKeys(keys []jellyfin.AuthenticationInfo, current string) []model.APIKey {
	out := make([]model.APIKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, publicAPIKey(key, current))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func publicAPIKey(key jellyfin.AuthenticationInfo, current string) model.APIKey {
	return model.APIKey{ID: apiKeyID(key.AccessToken), Name: key.AppName, Active: key.IsActive, IsRemora: key.AccessToken == current}
}

func apiKeyID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:8])
}

func (s *Supervisor) recordEvent(event model.Event) {
	s.mu.Lock()
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	event.State = s.status.State
	s.recordEventLocked(event)
	s.mu.Unlock()
}

func (s *Supervisor) stop(ctx context.Context, force bool) error {
	s.transition(model.StateStopping, "")
	if !force {
		shutdownCtx, cancel := context.WithTimeout(ctx, s.cfg.Remora.IOTimeout.Duration)
		if err := s.client.Shutdown(shutdownCtx, s.currentAPIKey()); err != nil {
			s.log.Debug("Jellyfin API shutdown unavailable; falling back to process signal", "error", err)
		}
		cancel()
	}
	err := s.process.Stop(ctx, force, s.cfg.Remora.ServerStopTimeout.Duration)
	_, stillRunning := s.process.Info(ctx)
	if !stillRunning {
		_ = s.process.RemovePIDFile()
		s.mu.Lock()
		s.status.PID = 0
		s.status.ProcessState = ""
		s.status.CPUPercent = 0
		s.status.MemoryBytes = 0
		s.status.FFmpegProcesses = 0
		s.status.ActiveTranscodes = 0
		s.mu.Unlock()
	}
	s.wasRunning = stillRunning
	return err
}
func (s *Supervisor) recordCrash() {
	now := time.Now()
	cut := now.Add(-10 * time.Minute)
	// Reuse the backing array only after reading each old entry; kept never
	// aliases data that is still needed by this loop.
	kept := s.crashes[:0]
	for _, t := range s.crashes {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.crashes = append(kept, now)
	if len(s.crashes) >= 5 {
		s.processFailed = true
	}
}
func (s *Supervisor) scheduleRestart() {
	n := len(s.crashes)
	if n < 1 {
		n = 1
	}
	s.nextStart = time.Now().Add(retryDelay(n))
}

func retryDelay(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := time.Second * time.Duration(1<<min(failures-1, 6))
	return min(delay, 60*time.Second)
}
func (s *Supervisor) clearOneShots() {
	s.mu.Lock()
	s.forceStop = false
	s.restartRequested = false
	s.mu.Unlock()
}

func (s *Supervisor) Status() model.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.status
	st.Storage = append([]model.StorageResult(nil), st.Storage...)
	st.Arguments = append([]string(nil), st.Arguments...)
	st.Sessions = append([]model.Session(nil), st.Sessions...)
	if st.PID > 0 && !s.process.StartedAt().IsZero() {
		st.ProcessStarted = s.process.StartedAt()
		st.UptimeSeconds = int64(time.Since(st.ProcessStarted).Seconds())
	}
	users := make(map[string]bool)
	st.ActiveTranscodes = 0
	for _, session := range st.Sessions {
		if session.User != "" && (session.Status == "playing" || session.Status == "paused") {
			users[session.User] = true
		}
		if session.Transcoding {
			st.ActiveTranscodes++
		}
	}
	st.PlayingUsers = make([]string, 0, len(users))
	for username := range users {
		st.PlayingUsers = append(st.PlayingUsers, username)
	}
	sort.Strings(st.PlayingUsers)
	return st
}

func (s *Supervisor) Events(limit int) []model.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit < 1 || limit > len(s.events) {
		limit = len(s.events)
	}
	return append([]model.Event(nil), s.events[len(s.events)-limit:]...)
}

func runtimeIdentity(runAsUser string) (int, string) {
	uid := -1
	var account *user.User
	var err error
	if runAsUser != "" {
		account, err = user.Lookup(runAsUser)
	} else {
		account, err = user.Current()
	}
	if err != nil {
		return uid, runAsUser
	}
	if parsed, parseErr := strconv.Atoi(account.Uid); parseErr == nil {
		uid = parsed
	}
	return uid, account.Username
}
func (s *Supervisor) transition(state model.State, message string) {
	s.mu.Lock()
	if s.status.State != state {
		s.status.State = state
		s.status.LastTransition = time.Now()
		s.eventSequence++
		s.recordEventLocked(model.Event{Sequence: s.eventSequence, Timestamp: s.status.LastTransition, Type: "state_transition", State: state, Message: message})
		if message != "" {
			s.log.Info("state transition", "state", state, "reason", message)
		} else {
			s.log.Info("state transition", "state", state)
		}
	}
	if message != "" {
		s.status.LastError = message
	} else if state == model.StateRunning {
		s.status.LastError = ""
	}
	s.mu.Unlock()
}

func (s *Supervisor) recordEventLocked(event model.Event) {
	if event.Sequence == 0 {
		s.eventSequence++
		event.Sequence = s.eventSequence
	}
	s.events = append(s.events, event)
	if len(s.events) > 256 {
		copy(s.events, s.events[len(s.events)-256:])
		s.events = s.events[:256]
	}
}
func (s *Supervisor) setError(message string) {
	s.mu.Lock()
	s.status.LastError = message
	s.mu.Unlock()
	s.log.Error(message)
}
func (s *Supervisor) statusCopyUpdate(fn func(*model.Status)) {
	s.mu.Lock()
	fn(&s.status)
	s.mu.Unlock()
}

func (s *Supervisor) persist() error {
	st := s.Status()
	return s.persistState(st, s.databaseDamaged)
}

func (s *Supervisor) persistState(st model.Status, databaseDamaged bool) error {
	data, damage := encodeState(st, databaseDamaged)
	writer := s.writeStateFile
	if writer == nil {
		writer = atomicWrite
	}
	if err := writer(filepath.Join(runtimeStateDir(s.cfg), contract.StateFileName), data, 0640); err != nil {
		return err
	}
	if err := writer(s.localStatePath, data, 0640); err != nil {
		return err
	}
	if damage == 1 && !durableStatePathSafe(s.cfg.Remora.DataDir, st.Storage) {
		return nil
	}
	return writer(filepath.Join(s.cfg.Remora.DataDir, contract.StateFileName), data, 0640)
}

func (s *Supervisor) reloadPersistentSafetyState() error {
	if s.rollbackState != nil {
		if err := s.persist(); err != nil {
			return fmt.Errorf("restore rejected lifecycle operation: %w", err)
		}
		remover := s.removeStateFile
		if remover == nil {
			remover = os.Remove
		}
		if err := remover(s.rollbackJournalPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove rollback journal: %w", err)
		}
		s.rollbackState = nil
		s.stateRestorePending = false
		return nil
	}
	localState, _, localErr := readPersistedStateResult(s.localStatePath)
	durableState, _, durableErr := readPersistedStateResult(filepath.Join(s.cfg.Remora.DataDir, contract.StateFileName))
	if localErr != nil && !errors.Is(localErr, os.ErrNotExist) {
		return fmt.Errorf("read local state mirror: %w", localErr)
	}
	if durableErr != nil && !errors.Is(durableErr, os.ErrNotExist) {
		return fmt.Errorf("read durable state: %w", durableErr)
	}
	s.mu.Lock()
	if localState.ManualStop || durableState.ManualStop {
		s.status.ManualStop = true
		s.status.DesiredState = model.DesiredStopped
	}
	if localState.DatabaseDamaged || durableState.DatabaseDamaged {
		s.databaseDamaged = true
		s.status.Database.Damaged = true
	}
	s.stateRestorePending = false
	s.mu.Unlock()
	return nil
}

func (s *Supervisor) rollbackJournalPath() string { return s.localStatePath + ".rollback" }

func durableStatePathSafe(dataDir string, storage []model.StorageResult) bool {
	cleanDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return false
	}
	for _, result := range storage {
		if !result.Fatal {
			continue
		}
		if result.Target == "" {
			return false
		}
		cleanTarget, err := filepath.Abs(result.Target)
		if err != nil {
			return false
		}
		relative, err := filepath.Rel(cleanTarget, cleanDataDir)
		if err != nil || relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

func (s *Supervisor) persistBestEffort() {
	if err := s.persist(); err != nil {
		s.log.Error("cannot persist supervisor state", "error", err)
	}
}

func encodeState(st model.Status, databaseDamaged bool) ([]byte, int) {
	health := 1
	if st.Jellyfin.Healthy {
		health = 0
	}
	damage := 0
	for _, r := range st.Storage {
		if r.Fatal {
			damage = 1
			break
		}
		if !r.Healthy {
			damage = 2
		}
	}
	manual := 0
	if st.ManualStop {
		manual = 1
	}
	database := 0
	if databaseDamaged {
		database = 1
	}
	return []byte(fmt.Sprintf("%d\n%d\n%d\n%d\n", health, damage, manual, database)), damage
}

func runtimeStateDir(cfg *config.Config) string {
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(filepath.Clean(cfg.Remora.DataDir))))[:12]
	if os.Geteuid() == 0 {
		return filepath.Join("/var/run/jellyfin-remora", id)
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("jellyfin-remora-%d", os.Geteuid()), id)
}

func localPersistentStatePath(cfg *config.Config) string {
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(filepath.Clean(cfg.Remora.DataDir))))[:12]
	if os.Geteuid() == 0 {
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join("/Library/Application Support/Jellyfin Remora/state", id, contract.StateFileName)
		case "windows":
			base := os.Getenv("ProgramData")
			if base == "" {
				base = `C:\\ProgramData`
			}
			return filepath.Join(base, "Jellyfin Remora", "state", id, contract.StateFileName)
		default:
			return filepath.Join("/var/lib/jellyfin-remora/instances", id, contract.StateFileName)
		}
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = filepath.Join(os.TempDir(), fmt.Sprintf("jellyfin-remora-%d", os.Geteuid()))
	}
	return filepath.Join(base, "jellyfin-remora", "state", id, contract.StateFileName)
}

type persistedState struct {
	ManualStop      bool
	DatabaseDamaged bool
}

func readPersistedState(path string) persistedState {
	state, _, _ := readPersistedStateResult(path)
	return state
}

func readPersistedStateResult(path string) (persistedState, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return persistedState{}, false, err
	}
	state, err := parsePersistedStateResult(b)
	if err != nil {
		return persistedState{}, true, fmt.Errorf("parse %s: %w", path, err)
	}
	return state, true, nil
}

func parsePersistedState(b []byte) persistedState {
	state, _ := parsePersistedStateResult(b)
	return state
}

func parsePersistedStateResult(b []byte) (persistedState, error) {
	lines := strings.Fields(string(b))
	if len(lines) < 3 {
		return persistedState{}, errors.New("state is truncated")
	}
	values := make([]int, 4)
	for index := 0; index < min(len(lines), len(values)); index++ {
		value, err := strconv.Atoi(lines[index])
		if err != nil {
			return persistedState{}, fmt.Errorf("field %d is not an integer", index+1)
		}
		values[index] = value
	}
	if values[0] < 0 || values[0] > 1 || values[1] < 0 || values[1] > 2 || values[2] < 0 || values[2] > 1 || values[3] < 0 || values[3] > 1 {
		return persistedState{}, errors.New("state field is out of range")
	}
	return persistedState{ManualStop: values[2] == 1, DatabaseDamaged: values[3] == 1}, nil
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".remora-state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

var ErrStorageFenced = errors.New("required storage is unhealthy")
