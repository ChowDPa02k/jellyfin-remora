package procmanager

import (
	"context"
	"errors"
	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type stopFallbackBackend struct {
	platform.Backend
	running bool
	forces  []bool
}

type alreadyExitedBackend struct{ platform.Backend }

type reusedPIDBackend struct {
	platform.Backend
	info     platform.ProcessInfo
	signaled bool
	exited   int
}

func (b *reusedPIDBackend) ProcessExited(pid int) { b.exited = pid }

func (b *reusedPIDBackend) ProcessInfo(context.Context, int) (platform.ProcessInfo, error) {
	return b.info, nil
}

func (b *reusedPIDBackend) SignalGroup(int, bool) error {
	b.signaled = true
	return nil
}

func TestInfoRejectsReusedPIDGeneration(t *testing.T) {
	started := time.Now().Add(-time.Hour)
	backend := &reusedPIDBackend{info: platform.ProcessInfo{PID: 42, State: "R", StartedAt: started.Add(time.Minute)}}
	manager := &Manager{backend: backend, pid: 42, startedAt: started}
	if _, running := manager.Info(context.Background()); running {
		t.Fatal("reused PID was reported as the managed process")
	}
	if manager.PID() != 0 {
		t.Fatal("reused PID was not cleared")
	}
	if backend.exited != 42 {
		t.Fatalf("backend cleanup PID = %d, want 42", backend.exited)
	}
}

func TestStopDoesNotSignalReusedPIDGeneration(t *testing.T) {
	started := time.Now().Add(-time.Hour)
	backend := &reusedPIDBackend{info: platform.ProcessInfo{PID: 42, State: "R", StartedAt: started.Add(time.Minute)}}
	manager := &Manager{backend: backend, pid: 42, startedAt: started}
	if err := manager.Stop(context.Background(), true, time.Second); err != nil {
		t.Fatal(err)
	}
	if backend.signaled {
		t.Fatal("Stop signaled a process from a different PID generation")
	}
}

func (alreadyExitedBackend) SignalGroup(int, bool) error {
	return errors.New("no such process")
}

func (alreadyExitedBackend) ProcessInfo(context.Context, int) (platform.ProcessInfo, error) {
	return platform.ProcessInfo{}, errors.New("no such process")
}

func TestStopAcceptsProcessThatExitedBeforeSignal(t *testing.T) {
	manager := &Manager{backend: alreadyExitedBackend{}, pid: 42, startedAt: time.Now()}
	if err := manager.Stop(context.Background(), false, time.Second); err != nil {
		t.Fatalf("already-exited process reported as stop failure: %v", err)
	}
}

func (b *stopFallbackBackend) SignalGroup(_ int, force bool) error {
	b.forces = append(b.forces, force)
	if !force {
		return errors.New("graceful signal unavailable")
	}
	b.running = false
	return nil
}

func (b *stopFallbackBackend) ProcessInfo(_ context.Context, pid int) (platform.ProcessInfo, error) {
	if !b.running {
		return platform.ProcessInfo{}, errors.New("process exited")
	}
	return platform.ProcessInfo{PID: pid, State: "R", StartedAt: time.Unix(100, 0)}, nil
}

func TestStopForcesProcessWhenGracefulSignalIsUnavailable(t *testing.T) {
	backend := &stopFallbackBackend{running: true}
	manager := &Manager{backend: backend, pid: 42, startedAt: time.Unix(100, 0)}
	if err := manager.Stop(context.Background(), false, time.Second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backend.forces, []bool{false, true}) {
		t.Fatalf("signal force sequence = %v", backend.forces)
	}
}

type adoptionBackend struct {
	platform.Backend
	processes []platform.ProcessInfo
}

type adoptedAttacherBackend struct {
	adoptionBackend
	attached int
}

func (b *adoptedAttacherBackend) AttachAdoptedProcess(pid int) error {
	b.attached = pid
	return nil
}

func TestAdoptionUsesAdoptedProcessAttachment(t *testing.T) {
	want := time.Now().Add(-time.Hour)
	backend := &adoptedAttacherBackend{adoptionBackend: adoptionBackend{processes: []platform.ProcessInfo{{PID: 42, StartedAt: want}}}}
	m := &Manager{backend: backend, executable: "/jellyfin"}
	adopted, err := m.Adopt(context.Background())
	if err != nil || !adopted {
		t.Fatalf("Adopt() = %t, %v", adopted, err)
	}
	if backend.attached != 42 {
		t.Fatalf("AttachAdoptedProcess PID = %d, want 42", backend.attached)
	}
}

func (b adoptionBackend) FindProcesses(context.Context, string, []string) ([]platform.ProcessInfo, error) {
	return b.processes, nil
}

func TestAdoptionRetainsDiscoveredProcessStartTime(t *testing.T) {
	want := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	m := &Manager{backend: adoptionBackend{processes: []platform.ProcessInfo{{PID: 42, StartedAt: want}}}, executable: "/jellyfin"}
	adopted, err := m.Adopt(context.Background())
	if err != nil || !adopted {
		t.Fatalf("Adopt() = %t, %v", adopted, err)
	}
	if got := m.StartedAt(); !got.Equal(want) {
		t.Fatalf("StartedAt() = %s, want %s", got, want)
	}
}

func TestResolveExecutableAndBuildArgs(t *testing.T) {
	d := t.TempDir()
	exe := filepath.Join(d, platformExecutableCandidates()[0])
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveExecutable(d)
	wantExecutable, canonicalErr := filepath.EvalSymlinks(exe)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	if err != nil || got != wantExecutable {
		t.Fatalf("got=%q err=%v", got, err)
	}
	cfg := &config.Config{Jellyfin: config.JellyfinConfig{DataDir: "/d", ConfigDir: "/c", CacheDir: "/k", LogDir: "/l", Parameters: map[string]any{"hostwebclient": true}}}
	want := []string{"--datadir=/d", "--configdir=/c", "--cachedir=/k", "--logdir=/l", "--hostwebclient=true"}
	if args := buildArgs(cfg, ""); !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%v", args)
	}
}

func TestAppendEnvDefaultPreservesExplicitColorPreference(t *testing.T) {
	env := appendEnvDefault([]string{"PATH=/bin"}, "DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION", "1")
	if got := env[len(env)-1]; got != "DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=1" {
		t.Fatalf("default environment entry = %q", got)
	}
	explicit := []string{"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=0"}
	if got := appendEnvDefault(explicit, "DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION", "1"); !reflect.DeepEqual(got, explicit) {
		t.Fatalf("explicit environment was overwritten: %v", got)
	}
}

func TestMergeEnvironmentInheritsAndOverridesWithoutDuplicates(t *testing.T) {
	inherited := []string{"PATH=/usr/bin", "https_proxy=http://old:8080", "KEEP=value"}
	overrides := map[string]string{
		"HTTPS_PROXY": "http://127.0.0.1:7890",
		"NO_PROXY":    "localhost,127.0.0.1",
	}
	got := mergeEnvironment(inherited, overrides)
	want := []string{
		"PATH=/usr/bin",
		"HTTPS_PROXY=http://127.0.0.1:7890",
		"KEEP=value",
		"NO_PROXY=localhost,127.0.0.1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment=%v, want %v", got, want)
	}
}

func TestMergeEnvironmentAllowsExplicitEmptyValue(t *testing.T) {
	got := mergeEnvironment([]string{"HTTP_PROXY=http://old:8080"}, map[string]string{"HTTP_PROXY": ""})
	if want := []string{"HTTP_PROXY="}; !reflect.DeepEqual(got, want) {
		t.Fatalf("environment=%v, want %v", got, want)
	}
}

func TestPIDFileSystemCallFailuresAreReturned(t *testing.T) {
	injected := errors.New("injected filesystem failure")
	manager := &Manager{
		cfg: &config.Config{Remora: config.RemoraConfig{DataDir: t.TempDir()}},
		pid: 42,
		writeFile: func(string, []byte, os.FileMode) error {
			return injected
		},
		removeFile: func(string) error { return injected },
	}
	if err := manager.WritePIDFile(); !errors.Is(err, injected) {
		t.Fatalf("write error = %v", err)
	}
	if err := manager.RemovePIDFile(); !errors.Is(err, injected) {
		t.Fatalf("remove error = %v", err)
	}
}
