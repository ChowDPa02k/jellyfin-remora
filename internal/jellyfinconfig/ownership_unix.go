//go:build !windows

package jellyfinconfig

import (
	"os"
	"path/filepath"
	"syscall"
)

func preserveOwnership(temporary, destination string) error {
	reference := destination
	info, err := os.Stat(reference)
	if os.IsNotExist(err) {
		reference = filepath.Dir(destination)
		info, err = os.Stat(reference)
	}
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return os.Chown(temporary, int(stat.Uid), int(stat.Gid))
}
