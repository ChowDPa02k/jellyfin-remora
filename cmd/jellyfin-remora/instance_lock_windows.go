//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"golang.org/x/sys/windows"
)

type windowsInstanceLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireInstanceLock(cfg *config.Config) (io.Closer, error) {
	if err := os.MkdirAll(cfg.Remora.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create Remora data directory for instance lock: %w", err)
	}
	path := filepath.Join(cfg.Remora.DataDir, "jellyfin-remora.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open instance lock %s: %w", path, err)
	}
	lock := &windowsInstanceLock{file: file}
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &lock.overlapped,
	)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another jellyfin-remora instance owns %s: %w", path, err)
	}
	return lock, nil
}

func (l *windowsInstanceLock) Close() error {
	unlockErr := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
