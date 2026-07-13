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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
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

type Supervisor struct {
	mu                   sync.RWMutex
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
	healthyStorageRuns   int
	apiFailures          int
	tick                 uint64
	wasRunning           bool
	processFailed        bool
	crashes              []time.Time
	nextStart            time.Time
	hungSince            time.Time
	apiKey               string
	adminToken           string
	watchdogReady        bool
	watchdogFailed       bool
	wizardIncompleteRuns int
	sessionsInitialized  bool
}

func New(cfg *config.Config, pm ProcessManager, sc StorageChecker, jc *jellyfin.Client, log *slog.Logger) *Supervisor {
	now := time.Now()
	s := &Supervisor{cfg: cfg, process: pm, storage: sc, client: jc, configuration: jellyfinconfig.New(cfg), log: log, actions: make(chan Request, 32)}
	if b, err := os.ReadFile(filepath.Join(cfg.Remora.DataDir, ".remora_api_key")); err == nil {
		s.apiKey = strings.TrimSpace(string(b))
	}
	uid, username := runtimeIdentity(cfg.Jellyfin.RunAsUser)
	s.status = model.Status{State: model.StateInit, DesiredState: model.DesiredRunning, UID: uid, Username: username, Executable: pm.Executable(), Arguments: pm.Arguments(), Ports: []int{cfg.Jellyfin.Networking.ServerAddressSettings.LocalHTTPPort}, LastTransition: now}
	if manualStop(filepath.Join(runtimeStateDir(cfg), "jellyfin.state")) || manualStop(filepath.Join(cfg.Remora.DataDir, "jellyfin.state")) {
		s.status.ManualStop = true
		s.status.DesiredState = model.DesiredStopped
	}
	return s
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
	s.runStorageChecks(ctx, true)
	s.reconcile(ctx)
	ticker := time.NewTicker(s.cfg.Remora.HeartbeatInterval.Duration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.statusCopyUpdate(func(st *model.Status) { st.DesiredState = model.DesiredStopped })
			stopCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Remora.ServerStopTimeout.Duration+5*time.Second)
			defer cancel()
			_ = s.stop(stopCtx, false)
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
	s.mu.Lock()
	switch req.Action {
	case ActionStart:
		s.status.ManualStop = false
		s.status.DesiredState = model.DesiredRunning
		s.processFailed = false
		s.crashes = nil
		s.nextStart = time.Time{}
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
	case ActionHealthcheck:
	default:
		err = fmt.Errorf("unknown action %q", req.Action)
	}
	s.mu.Unlock()
	if req.Action == ActionHealthcheck {
		s.immediateHealthcheck()
	}
	_ = s.persist()
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
	_ = s.persist()
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
		s.status.ProcessState = pi.State
		if len(pi.Ports) > 0 {
			s.status.Ports = pi.Ports
		}
		s.mu.Unlock()
	}
	if s.wasRunning && !running {
		s.recordCrash()
		s.scheduleRestart()
	}
	if !running {
		s.mu.Lock()
		s.status.PID = 0
		s.status.ProcessState = ""
		s.status.CPUPercent = 0
		s.status.MemoryBytes = 0
		s.status.Sessions = nil
		s.mu.Unlock()
		s.sessionsInitialized = false
		_ = s.process.RemovePIDFile()
	}
	s.wasRunning = running
	if manual || desired == model.DesiredStopped {
		if running {
			_ = s.stop(ctx, force)
		} else {
			s.transition(model.StateStopped, "")
		}
		s.clearOneShots()
		_ = s.persist()
		return
	}
	if fatal {
		s.storageFenced = true
		s.healthyStorageRuns = 0
		if running {
			_ = s.stop(ctx, false)
		}
		s.transition(model.StateStorageFenced, "required storage is unhealthy")
		s.clearOneShots()
		_ = s.persist()
		return
	}
	if s.storageFenced {
		s.healthyStorageRuns++
		if s.healthyStorageRuns < s.cfg.Remora.RecoverySuccesses {
			s.transition(model.StateStorageFenced, fmt.Sprintf("storage recovery confirmation %d/%d", s.healthyStorageRuns, s.cfg.Remora.RecoverySuccesses))
			_ = s.persist()
			return
		}
		s.storageFenced = false
		s.healthyStorageRuns = 0
	}
	if s.processFailed {
		s.transition(model.StateProcessFailed, "restart rate limit exceeded")
		_ = s.persist()
		return
	}
	uninterruptible := running && (strings.Contains(pi.State, "D") || strings.Contains(pi.State, "U"))
	if uninterruptible {
		if s.hungSince.IsZero() {
			s.hungSince = time.Now()
		}
		if time.Since(s.hungSince) >= s.cfg.Remora.ServerStopTimeout.Duration {
			if err := s.stop(ctx, true); err != nil {
				s.processFailed = true
				s.transition(model.StateProcessFailed, "hung Jellyfin process could not be killed")
				_ = s.persist()
				return
			}
			s.recordCrash()
			s.scheduleRestart()
			s.transition(model.StateRestartBackoff, "Jellyfin remained in an uninterruptible process state")
			_ = s.persist()
			return
		}
		s.transition(model.StateDegraded, "Jellyfin is in an uninterruptible process state")
		_ = s.persist()
		return
	}
	s.hungSince = time.Time{}
	if restart && running {
		if err := s.stop(ctx, force); err != nil {
			s.processFailed = true
			s.transition(model.StateProcessFailed, "Jellyfin could not be stopped: "+err.Error())
			_ = s.persist()
			return
		}
		running = false
		s.scheduleRestart()
	}
	if !running {
		if time.Now().Before(s.nextStart) {
			s.transition(model.StateRestartBackoff, "")
			s.clearOneShots()
			_ = s.persist()
			return
		}
		configurationResult, configurationErr := s.configuration.Reconcile()
		if configurationErr != nil {
			s.processFailed = true
			s.transition(model.StateProcessFailed, "Jellyfin configuration reconciliation failed: "+configurationErr.Error())
			s.clearOneShots()
			_ = s.persist()
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
			s.apiFailures = 0
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
		_ = s.persist()
		return
	}
	age := time.Since(s.process.StartedAt())
	// Jellyfin 12 exposes a lightweight setup listener before its core and
	// database are ready. During an ordinary restart that listener can briefly
	// report StartupWizardCompleted=false for an already initialized server.
	// Only trust setup state after /health proves the full application is ready.
	readiness := s.client.Health(ctx)
	s.mu.Lock()
	s.status.Jellyfin = readiness
	s.mu.Unlock()
	var info jellyfin.PublicInfo
	var infoErr error
	if readiness.Healthy {
		info, infoErr = s.client.PublicInfo(ctx)
	} else {
		infoErr = errors.New("Jellyfin core is not ready")
	}
	if infoErr == nil && info.StartupWizardCompleted != nil && !*info.StartupWizardCompleted {
		s.wizardIncompleteRuns++
		if age < 10*time.Second || s.wizardIncompleteRuns < 3 {
			s.transition(model.StateStarting, "confirming incomplete Jellyfin startup wizard")
			_ = s.persist()
			return
		}
		s.transition(model.StateFirstStart, "")
		if err := s.initializeServer(ctx); err != nil {
			s.setError("Jellyfin first-start initialization: " + err.Error())
			_ = s.persist()
			return
		}
		if err := s.stop(ctx, false); err != nil {
			s.processFailed = true
			s.transition(model.StateProcessFailed, "initialized Jellyfin could not be stopped: "+err.Error())
			_ = s.persist()
			return
		}
		s.scheduleRestart()
		s.transition(model.StateRestartBackoff, "Jellyfin initialization completed; restarting")
		_ = s.persist()
		return
	}
	if infoErr != nil || info.StartupWizardCompleted == nil || *info.StartupWizardCompleted {
		s.wizardIncompleteRuns = 0
	}
	if infoErr == nil {
		s.mu.Lock()
		s.status.Version = info.Version
		s.status.ServerName = info.ServerName
		s.mu.Unlock()
	}
	if infoErr == nil && s.apiKey == "" {
		if err := s.ensureAPIKey(ctx); err != nil {
			s.log.Warn("cannot provision Remora API key", "error", err)
		}
	}
	if infoErr == nil && s.cfg.Remora.UserLoginWatchdog.Enabled && !s.watchdogReady {
		if err := s.ensureWatchdog(ctx); err != nil {
			s.watchdogFailed = true
			s.setError("login watchdog setup: " + err.Error())
		} else {
			s.watchdogReady = true
			s.watchdogFailed = false
		}
	}
	credentialHeartbeat := max(1, s.cfg.Remora.UserLoginWatchdog.Heartbeat)
	if !s.cfg.Remora.UserLoginWatchdog.Enabled {
		credentialHeartbeat = max(1, s.cfg.Remora.HealthAPIHeartbeat)
	}
	if infoErr == nil && s.apiKey != "" && s.tick%uint64(credentialHeartbeat) == 0 {
		if err := s.client.ValidateAPIKey(ctx, s.apiKey); err != nil {
			var apiErr *jellyfin.APIError
			if errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403) {
				s.apiKey = ""
				s.adminToken = ""
				s.watchdogReady = false
				if recoverErr := s.ensureAPIKey(ctx); recoverErr != nil {
					s.transition(model.StateDegraded, "Remora API key recovery failed: "+recoverErr.Error())
					_ = s.persist()
					return
				}
			} else {
				s.transition(model.StateDegraded, "Remora API key validation failed: "+err.Error())
				_ = s.persist()
				return
			}
		}
	}
	if infoErr == nil && s.watchdogReady && s.tick%uint64(max(1, s.cfg.Remora.UserLoginWatchdog.Heartbeat)) == 0 {
		if err := s.client.EnsureWatchdogUser(ctx, s.adminCredential(), s.cfg.Remora.UserLoginWatchdog); err != nil {
			s.watchdogFailed = true
			s.transition(model.StateDegraded, "login watchdog failed: "+err.Error())
			_ = s.persist()
			return
		}
		s.watchdogFailed = false
	}
	if infoErr == nil && s.apiKey != "" && (!s.sessionsInitialized || s.tick%uint64(max(1, s.cfg.Remora.HealthAPIHeartbeat)) == 0) {
		sessions, err := s.client.Sessions(ctx, s.apiKey)
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
	s.mu.RLock()
	lastHealthCheck := s.status.Jellyfin.CheckedAt
	s.mu.RUnlock()
	if s.tick%uint64(max(1, s.cfg.Remora.HealthAPIHeartbeat)) == 0 || age >= s.cfg.Remora.ServerStartTimeout.Duration && lastHealthCheck.IsZero() {
		h := s.client.Health(ctx)
		s.mu.Lock()
		s.status.Jellyfin = h
		s.mu.Unlock()
		if h.Healthy {
			s.apiFailures = 0
		} else {
			s.apiFailures++
		}
	}
	s.mu.RLock()
	health := s.status.Jellyfin
	s.mu.RUnlock()
	if health.Healthy {
		if s.watchdogFailed {
			s.transition(model.StateDegraded, "login watchdog remains unhealthy")
		} else if degraded {
			s.transition(model.StateDegraded, "non-fatal storage degradation")
		} else {
			s.transition(model.StateRunning, "")
		}
		s.crashes = nil
	} else if age < s.cfg.Remora.ServerStartTimeout.Duration {
		s.transition(model.StateStarting, health.Error)
	} else if s.apiFailures >= s.cfg.Remora.APIFailureThreshold {
		if err := s.stop(ctx, false); err != nil {
			s.processFailed = true
			s.transition(model.StateProcessFailed, "unhealthy Jellyfin could not be stopped: "+err.Error())
			_ = s.persist()
			return
		}
		s.recordCrash()
		s.scheduleRestart()
		s.transition(model.StateRestartBackoff, "Jellyfin health check failed")
	} else {
		s.transition(model.StateDegraded, health.Error)
	}
	s.clearOneShots()
	_ = s.persist()
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
	s.adminToken = auth.AccessToken
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
		s.adminToken = auth.AccessToken
		credential = s.adminToken
	}
	key, err := s.client.EnsureAPIKey(ctx, credential)
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.cfg.Remora.DataDir, ".remora_api_key"), []byte(key+"\n"), 0600); err != nil {
		return err
	}
	s.apiKey = key
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
	if s.adminToken != "" {
		return s.adminToken
	}
	return s.apiKey
}

func (s *Supervisor) stop(ctx context.Context, force bool) error {
	s.transition(model.StateStopping, "")
	err := s.process.Stop(ctx, force, s.cfg.Remora.ServerStopTimeout.Duration)
	_, stillRunning := s.process.Info(ctx)
	if !stillRunning {
		_ = s.process.RemovePIDFile()
		s.mu.Lock()
		s.status.PID = 0
		s.status.ProcessState = ""
		s.status.CPUPercent = 0
		s.status.MemoryBytes = 0
		s.mu.Unlock()
	}
	s.wasRunning = stillRunning
	return err
}
func (s *Supervisor) recordCrash() {
	now := time.Now()
	cut := now.Add(-10 * time.Minute)
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
	delay := time.Second * time.Duration(1<<min(n-1, 6))
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	s.nextStart = time.Now().Add(delay)
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
		st.UptimeSeconds = int64(time.Since(s.process.StartedAt()).Seconds())
	}
	return st
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
		s.log.Info("state transition", "state", state)
	}
	if message != "" {
		s.status.LastError = message
	} else if state == model.StateRunning {
		s.status.LastError = ""
	}
	s.mu.Unlock()
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
	data := []byte(fmt.Sprintf("%d\n%d\n%d\n", health, damage, manual))
	if err := atomicWrite(filepath.Join(runtimeStateDir(s.cfg), "jellyfin.state"), data, 0640); err != nil {
		return err
	}
	if damage == 1 {
		return nil
	}
	return atomicWrite(filepath.Join(s.cfg.Remora.DataDir, "jellyfin.state"), data, 0640)
}

func runtimeStateDir(cfg *config.Config) string {
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(filepath.Clean(cfg.Remora.DataDir))))[:12]
	if os.Geteuid() == 0 {
		return filepath.Join("/var/run/jellyfin-remora", id)
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("jellyfin-remora-%d", os.Geteuid()), id)
}
func manualStop(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Fields(string(b))
	if len(lines) < 3 {
		return false
	}
	v, _ := strconv.Atoi(lines[2])
	return v == 1
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
