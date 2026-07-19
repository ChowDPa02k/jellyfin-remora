//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

var setConfigurationFileOwner = func(file *os.File, uid, gid int) error {
	return file.Chown(uid, gid)
}

func replaceConfigurationFile(path string, data []byte, mode os.FileMode, original os.FileInfo) error {
	stat, ok := original.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect configuration owner: unsupported file metadata")
	}
	uid, gid := int(stat.Uid), int(stat.Gid)
	return atomicWriteFilePrepared(path, data, mode, func(file *os.File) error {
		if err := setConfigurationFileOwner(file, uid, gid); err != nil {
			return fmt.Errorf("preserve configuration owner %d:%d: %w", uid, gid, err)
		}
		return nil
	})
}
