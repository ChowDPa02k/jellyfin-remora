package probe

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var writePayload = []byte("jellyfin-remora-storage-probe\n")

func Path(path, permission string) error {
	return PathOwned(path, permission, "")
}

func PathOwned(path, permission, cleanupToken string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open target: %w", err)
	}
	_, readErr := f.Readdirnames(1)
	closeErr := f.Close()
	if readErr != nil && readErr != io.EOF {
		return fmt.Errorf("read target: %w", readErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if permission == "r" {
		return nil
	}
	if err := ensureWriteCapacity(path, uint64(len(writePayload))); err != nil {
		return err
	}
	var tmp *os.File
	if cleanupToken == "" {
		tmp, err = os.CreateTemp(path, ".remora-probe-*")
	} else {
		if len(cleanupToken) != 32 {
			return errors.New("invalid probe cleanup token")
		}
		if _, decodeErr := hex.DecodeString(cleanupToken); decodeErr != nil {
			return errors.New("invalid probe cleanup token")
		}
		tmp, err = os.OpenFile(filepath.Join(path, ".remora-probe-"+cleanupToken), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	}
	if err != nil {
		return fmt.Errorf("create probe: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(writePayload); err != nil {
		tmp.Close()
		return fmt.Errorf("write probe: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync probe: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close probe: %w", err)
	}
	if err = os.Remove(name); err != nil {
		return fmt.Errorf("remove probe: %w", err)
	}
	parent, err := os.Open(filepath.Clean(path))
	if err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return nil
}
