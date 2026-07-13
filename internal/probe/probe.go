package probe

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func Path(path, permission string) error {
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
	tmp, err := os.CreateTemp(path, ".remora-probe-*")
	if err != nil {
		return fmt.Errorf("create probe: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write([]byte("jellyfin-remora-storage-probe\n")); err != nil {
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
