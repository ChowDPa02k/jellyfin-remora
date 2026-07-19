//go:build !windows && !linux && !darwin

package main

import (
	"fmt"
	"net"
)

func defaultLocalControlDiscoveryDirectories() []string { return []string{"/tmp"} }

func connectionPeerUID(net.Conn) (uint32, error) {
	return 0, fmt.Errorf("peer credentials are unsupported on this platform")
}
