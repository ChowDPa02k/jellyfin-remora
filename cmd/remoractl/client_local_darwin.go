//go:build darwin

package main

import (
	"net"

	"golang.org/x/sys/unix"
)

func defaultLocalControlDiscoveryDirectories() []string {
	return []string{"/var/run/jellyfin-remora", "/tmp"}
}

func connectionPeerUID(connection net.Conn) (uint32, error) {
	raw, err := rawConnection(connection)
	if err != nil {
		return 0, err
	}
	var uid uint32
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credential, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
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
