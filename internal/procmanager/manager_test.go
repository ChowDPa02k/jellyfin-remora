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
	return platform.ProcessInfo{PID: pid, State: "R"}, nil
}

func TestStopForcesProcessWhenGracefulSignalIsUnavailable(t *testing.T) {
	backend := &stopFallbackBackend{running: true}
	manager := &Manager{backend: backend, pid: 42}
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
