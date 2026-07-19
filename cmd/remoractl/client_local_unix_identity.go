//go:build !windows

package main

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

func socketOwnerUID(path string) (uint32, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("socket stat does not expose Unix ownership")
	}
	return stat.Uid, nil
}

func rawConnection(connection net.Conn) (syscall.RawConn, error) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("connection is %T, want *net.UnixConn", connection)
	}
	return unixConnection.SyscallConn()
}
