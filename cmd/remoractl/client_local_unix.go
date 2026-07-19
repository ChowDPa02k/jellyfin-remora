//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func defaultLocalControlEndpoint() string {
	return ""
}

var (
	localControlDiscoveryDirectories           = defaultLocalControlDiscoveryDirectories()
	localControlWarningWriter        io.Writer = os.Stderr
	legacyLocalControlDirectory                = "/tmp"
	unixSocketOwnerUID                         = socketOwnerUID
	unixConnectionPeerUID                      = connectionPeerUID
)

var errNoLocalControlSocket = errors.New("no Remora Unix socket found")

func newLocalClient(endpoint string) (*http.Client, string, error) {
	if endpoint == "" {
		var err error
		endpoint, err = discoverLocalControlEndpointFromDirectories(localControlDiscoveryDirectories)
		if err != nil {
			return nil, "", err
		}
		if filepath.Clean(filepath.Dir(endpoint)) == filepath.Clean(legacyLocalControlDirectory) {
			fmt.Fprintln(localControlWarningWriter, "WARNING: using legacy /tmp Remora socket fallback; migrate to a private runtime directory")
		}
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		connection, err := (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
		if err != nil {
			return nil, err
		}
		peerUID, err := unixConnectionPeerUID(connection)
		if err != nil {
			connection.Close()
			return nil, fmt.Errorf("verify Remora Unix socket peer: %w", err)
		}
		if !trustedLocalUID(peerUID) {
			connection.Close()
			return nil, fmt.Errorf("refusing Remora Unix socket peer uid %d", peerUID)
		}
		return connection, nil
	}}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}, "http://unix", nil
}

func discoverLocalControlEndpointFromDirectories(directories []string) (string, error) {
	for _, directory := range directories {
		endpoint, err := discoverLocalControlEndpoint(directory)
		if err == nil {
			return endpoint, nil
		}
		if !errors.Is(err, errNoLocalControlSocket) {
			return "", err
		}
	}
	return "", fmt.Errorf("%w in %s; use --socket to specify one", errNoLocalControlSocket, strings.Join(directories, ", "))
}

func discoverLocalControlEndpoint(directory string) (string, error) {
	preferred := filepath.Join(directory, ".s.remora.8095")
	if isTrustedUnixSocket(preferred) {
		return preferred, nil
	}

	matches, err := filepath.Glob(filepath.Join(directory, ".s.remora.*"))
	if err != nil {
		return "", fmt.Errorf("find Remora Unix sockets: %w", err)
	}
	candidates := make([]string, 0, len(matches))
	for _, path := range matches {
		portText := strings.TrimPrefix(filepath.Base(path), ".s.remora.")
		port, parseErr := strconv.Atoi(portText)
		if parseErr == nil && port >= 1 && port <= 65535 && isTrustedUnixSocket(path) {
			candidates = append(candidates, path)
		}
	}
	if len(candidates) == 0 {
		for _, path := range []string{
			filepath.Join(directory, ".s.remora"),
			filepath.Join(directory, "remora.sock"),
			filepath.Join(directory, "jellyfin-remora.sock"),
		} {
			if isTrustedUnixSocket(path) {
				candidates = append(candidates, path)
			}
		}
	}
	sort.Strings(candidates)
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("%w in %s (expected .s.remora.<port>)", errNoLocalControlSocket, directory)
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("multiple Remora Unix sockets found: %s; use --socket to select one", strings.Join(candidates, ", "))
	}
}

func isTrustedUnixSocket(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return false
	}
	uid, err := unixSocketOwnerUID(path)
	return err == nil && trustedLocalUID(uid)
}

func trustedLocalUID(uid uint32) bool {
	current := uint32(os.Geteuid())
	return uid == 0 || uid == current
}
