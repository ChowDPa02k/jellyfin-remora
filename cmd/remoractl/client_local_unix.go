//go:build !windows

package main

import (
	"context"
	"fmt"
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

func newLocalClient(endpoint string) (*http.Client, string, error) {
	if endpoint == "" {
		var err error
		endpoint, err = discoverLocalControlEndpoint("/tmp")
		if err != nil {
			return nil, "", err
		}
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
	}}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}, "http://unix", nil
}

func discoverLocalControlEndpoint(directory string) (string, error) {
	preferred := filepath.Join(directory, ".s.remora.8095")
	if isUnixSocket(preferred) {
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
		if parseErr == nil && port >= 1 && port <= 65535 && isUnixSocket(path) {
			candidates = append(candidates, path)
		}
	}
	if len(candidates) == 0 {
		for _, path := range []string{
			filepath.Join(directory, ".s.remora"),
			filepath.Join(directory, "jellyfin-remora.sock"),
		} {
			if isUnixSocket(path) {
				candidates = append(candidates, path)
			}
		}
	}
	sort.Strings(candidates)
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("no Remora Unix socket found in %s (expected .s.remora.<port>); use --socket to specify one", directory)
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("multiple Remora Unix sockets found: %s; use --socket to select one", strings.Join(candidates, ", "))
	}
}

func isUnixSocket(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}
