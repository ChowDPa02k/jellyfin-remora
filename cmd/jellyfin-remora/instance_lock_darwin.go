//go:build darwin

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func acquireInstanceLock(socketPath string) (io.Closer, error) {
	lockPath := socketPath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another jellyfin-remora instance already owns %s", socketPath)
	}
	return f, nil
}
