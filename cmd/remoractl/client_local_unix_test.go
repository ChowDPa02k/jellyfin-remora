//go:build !windows

package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverLocalControlEndpointPrefersDefaultPort(t *testing.T) {
	directory := t.TempDir()
	preferred := listenUnixSocket(t, filepath.Join(directory, ".s.remora.8095"))
	listenUnixSocket(t, filepath.Join(directory, ".s.remora.9000"))

	got, err := discoverLocalControlEndpoint(directory)
	if err != nil {
		t.Fatal(err)
	}
	if got != preferred {
		t.Fatalf("endpoint = %q, want %q", got, preferred)
	}
}

func TestDiscoverLocalControlEndpointFindsUniqueCustomPort(t *testing.T) {
	directory := t.TempDir()
	want := listenUnixSocket(t, filepath.Join(directory, ".s.remora.18095"))

	got, err := discoverLocalControlEndpoint(directory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestDiscoverLocalControlEndpointRejectsAmbiguousCustomPorts(t *testing.T) {
	directory := t.TempDir()
	listenUnixSocket(t, filepath.Join(directory, ".s.remora.18095"))
	listenUnixSocket(t, filepath.Join(directory, ".s.remora.18096"))

	_, err := discoverLocalControlEndpoint(directory)
	if err == nil || !strings.Contains(err.Error(), "multiple Remora Unix sockets") {
		t.Fatalf("error = %v", err)
	}
}

func TestDiscoverLocalControlEndpointSupportsLegacySocket(t *testing.T) {
	directory := t.TempDir()
	want := listenUnixSocket(t, filepath.Join(directory, ".s.remora"))

	got, err := discoverLocalControlEndpoint(directory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func listenUnixSocket(t *testing.T, path string) string {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(path)
	})
	return path
}
