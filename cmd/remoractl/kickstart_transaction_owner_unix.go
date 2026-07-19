//go:build !windows

package main

import (
	"os"
	"syscall"
)

type kickstartPathOwner struct{ uid, gid int }

func captureKickstartPathOwner(info os.FileInfo) any {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return kickstartPathOwner{uid: int(stat.Uid), gid: int(stat.Gid)}
}

func restoreKickstartPathOwner(path string, owner any) error {
	value, ok := owner.(kickstartPathOwner)
	if !ok {
		return nil
	}
	return os.Chown(path, value.uid, value.gid)
}
