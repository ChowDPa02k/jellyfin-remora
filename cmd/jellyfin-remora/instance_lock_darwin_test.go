//go:build darwin

package main

import (
	"path/filepath"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func TestInstanceLockRejectsDuplicateAndCanBeReacquired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remora.sock")
	cfg := &config.Config{RESTAPI: config.RESTAPIConfig{UnixSocket: path}}
	one, err := acquireInstanceLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if two, err := acquireInstanceLock(cfg); err == nil {
		_ = two.Close()
		t.Fatal("duplicate instance acquired lock")
	}
	if err := one.Close(); err != nil {
		t.Fatal(err)
	}
	three, err := acquireInstanceLock(cfg)
	if err != nil {
		t.Fatalf("lock was not reusable: %v", err)
	}
	_ = three.Close()
}
