//go:build !windows

package config

import "testing"

func TestDefaultUnixSocketIncludesRESTPort(t *testing.T) {
	rest := RESTAPIConfig{Port: 18095}
	defaultPlatformControl(&rest)
	if rest.UnixSocket != "/tmp/.s.remora.18095" {
		t.Fatalf("Unix socket = %q", rest.UnixSocket)
	}
}

func TestExplicitUnixSocketIsPreserved(t *testing.T) {
	rest := RESTAPIConfig{Port: 18095, UnixSocket: "/private/remora.sock"}
	defaultPlatformControl(&rest)
	if rest.UnixSocket != "/private/remora.sock" {
		t.Fatalf("Unix socket = %q", rest.UnixSocket)
	}
}
