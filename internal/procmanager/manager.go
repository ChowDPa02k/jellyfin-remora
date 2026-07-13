package procmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
)

type Manager struct {
	mu         sync.Mutex
	cfg        *config.Config
	backend    platform.Backend
	executable string
	args       []string
	pid        int
	startedAt  time.Time
	cmd        *exec.Cmd
	waitDone   chan error
	stdout     io.Writer
	stderr     io.Writer
}

func New(cfg *config.Config, backend platform.Backend, stdout, stderr io.Writer) (*Manager, error) {
	exe, err := resolveExecutable(cfg.Jellyfin.Path)
	if err != nil {
		return nil, err
	}
	webDir, err := resolveWebDir(exe, cfg.Jellyfin.WebDir)
	if err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, backend: backend, executable: exe, args: buildArgs(cfg, webDir), stdout: stdout, stderr: stderr}, nil
}

func resolveExecutable(path string) (string, error) {
	st, err := os.Stat(path)
	if err == nil && !st.IsDir() {
		if runtime.GOOS != "windows" && st.Mode()&0111 == 0 {
			return "", fmt.Errorf("Jellyfin executable is not executable: %s", path)
		}
		return canonicalExecutable(path)
	}
	for _, name := range []string{"Jellyfin", "jellyfin", "jellyfin.exe"} {
		p := filepath.Join(path, name)
		if st, e := os.Stat(p); e == nil && !st.IsDir() {
			if runtime.GOOS != "windows" && st.Mode()&0111 == 0 {
				return "", fmt.Errorf("Jellyfin executable is not executable: %s", p)
			}
			return canonicalExecutable(p)
		}
	}
	return "", fmt.Errorf("Jellyfin executable not found under %s", path)
}

func canonicalExecutable(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Jellyfin executable path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve Jellyfin executable symlinks: %w", err)
	}
	return resolved, nil
}

func resolveWebDir(executable, configured string) (string, error) {
	if configured != "default" {
		return configured, nil
	}
	macOSDir := filepath.Dir(executable)
	if filepath.Base(macOSDir) != "MacOS" || filepath.Base(filepath.Dir(macOSDir)) != "Contents" {
		return "", nil
	}
	candidate := filepath.Clean(filepath.Join(macOSDir, "..", "Resources", "jellyfin-web"))
	if st, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !st.IsDir() {
		return candidate, nil
	}
	return "", fmt.Errorf("Jellyfin web resources not found under app bundle: %s", candidate)
}

func buildArgs(cfg *config.Config, webDir string) []string {
	a := []string{"--datadir=" + cfg.Jellyfin.DataDir, "--configdir=" + cfg.Jellyfin.ConfigDir, "--cachedir=" + cfg.Jellyfin.CacheDir, "--logdir=" + cfg.Jellyfin.LogDir}
	if webDir != "" {
		a = append(a, "--webdir="+webDir)
	}
	keys := make([]string, 0, len(cfg.Jellyfin.Parameters))
	for k := range cfg.Jellyfin.Parameters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a = append(a, "--"+strings.ToLower(k)+"="+fmt.Sprint(cfg.Jellyfin.Parameters[k]))
	}
	return a
}

func (m *Manager) Executable() string   { return m.executable }
func (m *Manager) Arguments() []string  { return append([]string(nil), m.args...) }
func (m *Manager) PID() int             { m.mu.Lock(); defer m.mu.Unlock(); return m.pid }
func (m *Manager) StartedAt() time.Time { m.mu.Lock(); defer m.mu.Unlock(); return m.startedAt }

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pid > 0 {
		return errors.New("Jellyfin is already managed")
	}
	cmd := exec.Command(m.executable, m.args...)
	cmd.Env = os.Environ()
	cmd.Stdout = m.stdout
	cmd.Stderr = m.stderr
	if err := m.backend.ConfigureProcess(cmd, m.cfg.Jellyfin.RunAsUser, m.cfg.Jellyfin.RunAsGroup); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Jellyfin: %w", err)
	}
	m.cmd = cmd
	m.pid = cmd.Process.Pid
	m.startedAt = time.Now()
	m.waitDone = make(chan error, 1)
	pid := m.pid
	done := m.waitDone
	go func() {
		err := cmd.Wait()
		done <- err
		close(done)
		m.mu.Lock()
		if m.pid == pid {
			m.pid = 0
			m.cmd = nil
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *Manager) Adopt(ctx context.Context) (bool, error) {
	m.mu.Lock()
	if m.pid > 0 {
		m.mu.Unlock()
		return true, nil
	}
	m.mu.Unlock()
	processes, err := m.backend.FindProcesses(ctx, m.executable, m.args)
	if err != nil {
		return false, err
	}
	if len(processes) == 0 {
		return false, nil
	}
	if len(processes) > 1 {
		return false, fmt.Errorf("multiple matching Jellyfin processes found")
	}
	m.mu.Lock()
	m.pid = processes[0].PID
	m.startedAt = time.Now()
	m.mu.Unlock()
	return true, nil
}

func (m *Manager) Info(ctx context.Context) (platform.ProcessInfo, bool) {
	m.mu.Lock()
	pid := m.pid
	m.mu.Unlock()
	if pid <= 0 {
		return platform.ProcessInfo{}, false
	}
	pi, err := m.backend.ProcessInfo(ctx, pid)
	if err != nil || strings.Contains(pi.State, "Z") {
		m.mu.Lock()
		if m.pid == pid {
			m.pid = 0
		}
		m.mu.Unlock()
		return platform.ProcessInfo{}, false
	}
	return pi, true
}

func (m *Manager) Stop(ctx context.Context, force bool, timeout time.Duration) error {
	m.mu.Lock()
	pid := m.pid
	m.mu.Unlock()
	if pid <= 0 {
		return nil
	}
	if err := m.backend.SignalGroup(pid, force); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if force {
		return m.waitForExit(ctx, pid, 5*time.Second)
	}
	if err := m.waitForExit(ctx, pid, timeout); err == nil {
		return nil
	}
	if err := m.backend.SignalGroup(pid, true); err != nil {
		return err
	}
	return m.waitForExit(ctx, pid, 5*time.Second)
}

func (m *Manager) waitForExit(ctx context.Context, pid int, timeout time.Duration) error {
	t := time.NewTimer(timeout)
	defer t.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return fmt.Errorf("process %d did not exit within %s", pid, timeout)
		case <-tick.C:
			if _, ok := m.Info(ctx); !ok {
				return nil
			}
		}
	}
}

func (m *Manager) WritePIDFile() error {
	pid := m.PID()
	if pid <= 0 {
		return errors.New("no managed PID")
	}
	return atomicWrite(filepath.Join(m.cfg.Remora.DataDir, "jellyfin.pid"), []byte(strconv.Itoa(pid)+"\n"), 0640)
}
func (m *Manager) RemovePIDFile() error {
	err := os.Remove(filepath.Join(m.cfg.Remora.DataDir, "jellyfin.pid"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
