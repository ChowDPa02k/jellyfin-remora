//go:build linux

package probe

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func ensureWriteCapacity(path string, required uint64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return fmt.Errorf("inspect probe free space: %w", err)
	}
	blockSize := uint64(stat.Frsize)
	if blockSize == 0 {
		blockSize = uint64(stat.Bsize)
	}
	available := stat.Bavail * blockSize
	if available < required {
		return fmt.Errorf("insufficient free space for probe: %d bytes available, need %d", available, required)
	}
	return nil
}
