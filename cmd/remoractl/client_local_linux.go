//go:build linux

package main

import (
	"net"

	"github.com/ChowDPa02K/jellyfin-remora/internal/contract"
	"golang.org/x/sys/unix"
)

func defaultLocalControlDiscoveryDirectories() []string {
	return []string{contract.LinuxRuntimeDir, "/tmp"}
}

func connectionPeerUID(connection net.Conn) (uint32, error) {
	raw, err := rawConnection(connection)
	if err != nil {
		return 0, err
	}
	var uid uint32
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credential, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err != nil {
			controlErr = err
			return
		}
		uid = credential.Uid
	}); err != nil {
		return 0, err
	}
	return uid, controlErr
}
