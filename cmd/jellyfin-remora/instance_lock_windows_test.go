//go:build windows

package main

import (
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func TestWindowsInstanceLockExcludesDuplicateAndReleases(t *testing.T) {
	cfg := &config.Config{Remora: config.RemoraConfig{DataDir: t.TempDir()}}
	one, err := acquireInstanceLock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if two, err := acquireInstanceLock(cfg); err == nil {
		_ = two.Close()
		t.Fatal("second instance acquired the same Windows lock")
	}
	if err := one.Close(); err != nil {
		t.Fatal(err)
	}
	three, err := acquireInstanceLock(cfg)
	if err != nil {
		t.Fatalf("lock remained held after close: %v", err)
	}
	_ = three.Close()
}
