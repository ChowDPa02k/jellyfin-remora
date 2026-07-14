//go:build !windows

package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func defaultLocalControlEndpoint() string {
	return filepath.Join(os.TempDir(), "jellyfin-remora.sock")
}

func newLocalClient(endpoint string) (*http.Client, string, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
	}}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}, "http://unix", nil
}
