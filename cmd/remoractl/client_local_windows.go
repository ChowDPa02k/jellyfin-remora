//go:build windows

package main

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/contract"
	"github.com/Microsoft/go-winio"
)

func defaultLocalControlEndpoint() string {
	return contract.WindowsNamedPipe
}

func newLocalClient(endpoint string) (*http.Client, string, error) {
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		timeout := 10 * time.Second
		return winio.DialPipe(endpoint, &timeout)
	}}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}, "http://pipe", nil
}
