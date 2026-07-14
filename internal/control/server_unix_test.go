//go:build !windows

package control

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func TestUnixSocketPermissionsAndDaemonRestart(t *testing.T) {
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("remora-control-%d.sock", time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(socket) })
	for attempt := 0; attempt < 2; attempt++ {
		cfg := &config.Config{RESTAPI: config.RESTAPIConfig{Listen: "127.0.0.1", Port: 0, UnixSocket: socket}}
		server := New(cfg, &fakeController{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- server.Run(ctx) }()
		deadline := time.Now().Add(2 * time.Second)
		for {
			info, err := os.Stat(socket)
			if err == nil {
				if info.Mode().Perm() != 0o660 {
					t.Fatalf("socket mode=%o", info.Mode().Perm())
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("socket did not appear: %v", err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestSafeRemoveSocketRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	if err := os.WriteFile(path, []byte("do not delete"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := safeRemoveSocket(path); err == nil {
		t.Fatal("expected regular-file rejection")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("regular file was removed: %v", err)
	}
}
