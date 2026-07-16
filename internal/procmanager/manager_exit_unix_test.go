//go:build !windows

package procmanager

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
)

type exitNotificationBackend struct {
	platform.Backend
	exited chan int
}

func (*exitNotificationBackend) ConfigureProcess(*exec.Cmd, string, string) error { return nil }
func (b *exitNotificationBackend) ProcessExited(pid int)                          { b.exited <- pid }

func TestStartedProcessExitNotifiesPlatformBackend(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "exit-success")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend := &exitNotificationBackend{exited: make(chan int, 1)}
	manager := &Manager{
		cfg: &config.Config{}, backend: backend, executable: executable,
		stdout: io.Discard, stderr: io.Discard,
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-backend.exited:
		if got <= 0 {
			t.Fatalf("ProcessExited(%d)", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("platform backend was not notified after process exit")
	}
}
