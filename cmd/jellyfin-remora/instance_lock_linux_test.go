//go:build linux

package main

import (
	"path/filepath"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func TestLinuxInstanceLockIsExclusiveAndReusable(t *testing.T) {
	cfg := &config.Config{RESTAPI: config.RESTAPIConfig{UnixSocket: filepath.Join(t.TempDir(), "remora.sock")}}
	first, err := acquireInstanceLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireInstanceLock(cfg); err == nil {
		t.Fatal("second instance acquired the same lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireInstanceLock(cfg)
	if err != nil {
		t.Fatalf("lock was not reusable after close: %v", err)
	}
	_ = third.Close()
}
