//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"golang.org/x/sys/unix"
)

func acquireInstanceLock(cfg *config.Config) (io.Closer, error) {
	lockPath := cfg.RESTAPI.UnixSocket + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o750); err != nil {
		return nil, fmt.Errorf("create instance-lock directory: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another jellyfin-remora instance already owns %s", cfg.RESTAPI.UnixSocket)
	}
	return f, nil
}
