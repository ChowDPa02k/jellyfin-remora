//go:build !windows

package control

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
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

func TestExplicitTCPDisableOnlyStartsUnixSocket(t *testing.T) {
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { _ = reservation.Close() })

	socket := filepath.Join(os.TempDir(), fmt.Sprintf("remora-tcp-disabled-%d.sock", time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(socket) })
	cfg := &config.Config{RESTAPI: config.RESTAPIConfig{
		Listen: "127.0.0.1", Port: port, UnixSocket: socket,
		TCPEnabled: config.Optional[bool]{Set: true, Value: false},
	}}
	server := New(cfg, &fakeController{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		if runErr := <-done; runErr != nil {
			t.Errorf("server shutdown: %v", runErr)
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !isSocket(socket) {
		if time.Now().After(deadline) {
			t.Fatal("Unix socket did not appear")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("TCP listener accepted a connection while disabled")
	}
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}
