//go:build !windows

package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLocalClientDiscoversAndUsesSocketFromRuntimeDirectory(t *testing.T) {
	directory := unixSocketTestDir(t)
	oldDirectory := localControlDiscoveryDirectory
	localControlDiscoveryDirectory = directory
	t.Cleanup(func() { localControlDiscoveryDirectory = oldDirectory })

	path := filepath.Join(directory, ".s.remora.18095")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"state":"RUNNING"}`))
	})}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		<-serveDone
		_ = os.Remove(path)
	})

	client, base, err := newLocalClient("")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(base + "/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d", resp.StatusCode)
	}
}

func TestDiscoverLocalControlEndpointPrefersDefaultPort(t *testing.T) {
	directory := unixSocketTestDir(t)
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
	directory := unixSocketTestDir(t)
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
	directory := unixSocketTestDir(t)
	listenUnixSocket(t, filepath.Join(directory, ".s.remora.18095"))
	listenUnixSocket(t, filepath.Join(directory, ".s.remora.18096"))

	_, err := discoverLocalControlEndpoint(directory)
	if err == nil || !strings.Contains(err.Error(), "multiple Remora Unix sockets") {
		t.Fatalf("error = %v", err)
	}
}

func TestDiscoverLocalControlEndpointSupportsLegacySocket(t *testing.T) {
	directory := unixSocketTestDir(t)
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

func unixSocketTestDir(t *testing.T) string {
	t.Helper()
	// macOS limits Unix-domain socket paths to roughly 104 bytes. t.TempDir()
	// expands below /var/folders and can exceed that limit before the socket
	// filename is appended, unlike Remora's real short /tmp runtime directory.
	directory, err := os.MkdirTemp("/tmp", "remora-socket-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
