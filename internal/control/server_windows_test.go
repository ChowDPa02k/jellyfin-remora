//go:build windows

package control

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/Microsoft/go-winio"
)

func TestWindowsNamedPipeACLIncludesCurrentIdentity(t *testing.T) {
	sddl, err := localPipeSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sddl, "(A;;GA;;;S-1-") || !strings.Contains(sddl, "(A;;GA;;;SY)") || !strings.Contains(sddl, "(A;;GA;;;BA)") {
		t.Fatalf("named-pipe SDDL = %q", sddl)
	}
}

func TestWindowsNamedPipeRoundTripAndRestart(t *testing.T) {
	pipe := `\\.\pipe\jellyfin-remora-test-` + strconv.FormatInt(time.Now().UnixNano(), 10)
	for attempt := 0; attempt < 2; attempt++ {
		cfg := &config.Config{RESTAPI: config.RESTAPIConfig{
			Listen:    "127.0.0.1",
			Port:      0,
			NamedPipe: pipe,
		}}
		server := New(cfg, &fakeController{status: model.Status{State: model.StateRunning}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- server.Run(ctx) }()

		client := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
			timeout := 100 * time.Millisecond
			return winio.DialPipe(pipe, &timeout)
		}}, Timeout: time.Second}
		deadline := time.Now().Add(3 * time.Second)
		var response *http.Response
		var err error
		for time.Now().Before(deadline) {
			response, err = client.Get("http://pipe/v1/status")
			if err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			cancel()
			<-done
			t.Fatalf("named-pipe request failed: %v", err)
		}
		var status model.Status
		if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if status.State != model.StateRunning {
			t.Fatalf("state = %s", status.State)
		}
		client.CloseIdleConnections()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
