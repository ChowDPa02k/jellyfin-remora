//go:build windows

package probe

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func ensureWriteCapacity(path string, required uint64) error {
	root, err := windows.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("inspect probe free space: %w", err)
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(root, &available, nil, nil); err != nil {
		return fmt.Errorf("inspect probe free space: %w", err)
	}
	if available < required {
		return fmt.Errorf("insufficient free space for probe: %d bytes available, need %d", available, required)
	}
	return nil
}
