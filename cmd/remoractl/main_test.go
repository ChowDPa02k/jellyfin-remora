package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/jedib0t/go-pretty/v6/text"
)

func TestRenderStatusAlignsUnicodeTablesAndSanitizesCells(t *testing.T) {
	status := model.Status{
		State: model.StateRunning, DesiredState: model.DesiredRunning,
		UID: 501, Username: "zhoudingpeng", PID: 1904, Executable: "/Applications/Jellyfin.app/Contents/MacOS/Jellyfin",
		Version: "10.11.11", ServerName: "UAT-Test", Ports: []int{8096}, UptimeSeconds: 55,
		Storage:  []model.StorageResult{{Index: 0, Healthy: true, Type: "physical", Target: "/Users/zhoudingpeng/Appdata"}, {Index: 1, Healthy: true, Type: "smb", Target: "/Volumes/nas_STORAGE_公共空间"}},
		Sessions: []model.Session{{ID: "a1b2c3d4-full", Status: "playing", User: "alice", Device: "Jellyfin Web (Chrome)", Media: "The Matrix\n\x1b[31m"}},
	}
	output := renderStatus(status)
	for _, want := range []string{"| UID", "501 (zhoudingpeng)", "| 1 | true", "samba", "nas_STORAGE_公共空间", "| a1b2c3d4 | playing"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output omitted %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "\x1b") || strings.Contains(output, "\n\x1b") {
		t.Fatalf("terminal control sequence was not removed: %q", output)
	}
	for _, block := range strings.Split(strings.TrimSpace(output), "\n\n") {
		lines := strings.Split(block, "\n")
		wantWidth := text.StringWidth(lines[0])
		for _, line := range lines[1:] {
			if got := text.StringWidth(line); got != wantWidth {
				t.Fatalf("misaligned table width=%d want=%d line=%q\n%s", got, wantWidth, line, block)
			}
		}
	}
}

func TestRenderStatusOmitsActiveSessionsWhenEmpty(t *testing.T) {
	output := renderStatus(model.Status{State: model.StateStopped})
	if strings.Contains(output, "Active Sessions") {
		t.Fatalf("empty sessions table should be omitted:\n%s", output)
	}
	if !strings.Contains(output, "Jellyfin Status") || !strings.Contains(output, "Storage Volumes") {
		t.Fatalf("required status tables were omitted:\n%s", output)
	}
}

func TestRenderStatusColorsStateAndHealthOnlyWhenEnabled(t *testing.T) {
	status := model.Status{
		State: model.StateRunning,
		Storage: []model.StorageResult{
			{Index: 0, Healthy: true, Type: "physical", Target: "/Volumes/healthy"},
			{Index: 1, Healthy: false, Type: "smb", Target: "/Volumes/unhealthy"},
		},
	}
	plain := renderStatus(status)
	colored := renderStatusStyled(status, true)
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("plain status contains ANSI: %q", plain)
	}
	if !strings.Contains(colored, "\x1b[") || text.StripEscape(colored) != plain {
		t.Fatalf("colored status differs beyond ANSI\nplain:\n%s\ncolored:\n%q", plain, colored)
	}
}

func TestRenderStorageKeepsFullTargetAndOmitsLatency(t *testing.T) {
	target := "/Volumes/" + strings.Repeat("very-long-storage-path/", 8) + "media"
	output := renderStatus(model.Status{Storage: []model.StorageResult{{Index: 0, Healthy: true, Type: "physical", Target: target, LatencyMS: 9876}}})
	if !strings.Contains(output, target) {
		t.Fatalf("full target was not rendered:\n%s", output)
	}
	if strings.Contains(output, "latency") || strings.Contains(output, "9876ms") || strings.Contains(output, "…") {
		t.Fatalf("storage output retained latency or truncated target:\n%s", output)
	}
}

func TestWriteStatusJSONRetainsStructuredFields(t *testing.T) {
	status := model.Status{State: model.StateRunning, UID: 501, Username: "user", Sessions: []model.Session{{ID: "session"}}}
	var output bytes.Buffer
	if err := writeStatus(&output, status, true); err != nil {
		t.Fatal(err)
	}
	var decoded model.Status
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.UID != 501 || decoded.Username != "user" || len(decoded.Sessions) != 1 {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestWaitAllowsProcessFailedStateToConvergeAfterReset(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := model.StateProcessFailed
		if calls.Add(1) >= 2 {
			state = model.StateRunning
		}
		_ = json.NewEncoder(w).Encode(model.Status{State: state, DesiredState: model.DesiredRunning})
	}))
	defer srv.Close()
	if err := wait(srv.Client(), srv.URL, "start", 0, io.Discard, false); err != nil {
		t.Fatal(err)
	}
}

func TestWaitAllowsStorageRecoveryToConverge(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := model.StateStorageFenced
		if calls.Add(1) >= 3 {
			state = model.StateRunning
		}
		_ = json.NewEncoder(w).Encode(model.Status{State: state, DesiredState: model.DesiredRunning})
	}))
	defer srv.Close()
	if err := wait(srv.Client(), srv.URL, "start", 0, io.Discard, false); err != nil {
		t.Fatal(err)
	}
}

func TestRestartWaitsForPIDReplacement(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pid := 42
		if calls.Add(1) >= 3 {
			pid = 43
		}
		_ = json.NewEncoder(w).Encode(model.Status{State: model.StateRunning, DesiredState: model.DesiredRunning, PID: pid})
	}))
	defer srv.Close()
	if err := wait(srv.Client(), srv.URL, "restart", 42, io.Discard, false); err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 3 {
		t.Fatalf("restart returned before PID changed: calls=%d", calls.Load())
	}
}

func TestLocalhostClientPinsValidatedLoopbackAddress(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(model.Status{State: model.StateRunning})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	client, base, err := newClient("http://localhost:"+strconv.Itoa(listener.Addr().(*net.TCPAddr).Port), "")
	if err != nil {
		t.Fatal(err)
	}
	status, err := request(client, http.MethodGet, base+"/v1/status")
	if err != nil || status.State != model.StateRunning {
		t.Fatalf("request status=%+v err=%v", status, err)
	}
}

func TestLoopbackLiteralClientRejectsRedirect(t *testing.T) {
	redirected := false
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	defer destination.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/v1/status", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client, base, err := newClient(source.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = request(client, http.MethodGet, base+"/v1/status")
	if err == nil {
		t.Fatal("redirect response was accepted")
	}
	if redirected {
		t.Fatal("loopback IP literal client followed redirect")
	}
}

func TestRequestReturnsMalformedURLError(t *testing.T) {
	if _, err := request(http.DefaultClient, http.MethodGet, "://bad-url"); err == nil {
		t.Fatal("malformed URL succeeded")
	}
}

func TestRequestDecodesStructuredAPIErrorAndExitCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"code":"storage_fenced","message":"storage unavailable","operation_id":"op-42"}}`)
	}))
	defer server.Close()
	_, err := request(server.Client(), http.MethodPost, server.URL+"/v1/start")
	var apiErr *HTTPError
	if !errors.As(err, &apiErr) || apiErr.Code != "storage_fenced" || apiErr.OperationID != "op-42" {
		t.Fatalf("API error=%#v", err)
	}
	if code := exitCode(err); code != 4 {
		t.Fatalf("exit code=%d, want 4", code)
	}
}

func TestPersistenceUnavailableUsesRetryableExitCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"code":"persistence_unavailable","message":"state disk is full"}}`)
	}))
	defer server.Close()
	_, err := request(server.Client(), http.MethodPost, server.URL+"/v1/stop")
	if code := exitCode(err); code != 3 {
		t.Fatalf("exit code=%d, want 3 (error=%v)", code, err)
	}
}

func TestExitCodesAreDeterministic(t *testing.T) {
	for _, test := range []struct {
		err  error
		want int
	}{
		{err: &usageError{message: "usage"}, want: 2},
		{err: &HTTPError{StatusCode: http.StatusServiceUnavailable}, want: 3},
		{err: errOperationTimedOut, want: 5},
		{err: errors.New("internal"), want: 1},
	} {
		if got := exitCode(test.err); got != test.want {
			t.Fatalf("exitCode(%v)=%d, want %d", test.err, got, test.want)
		}
	}
}

func TestRenderEventsSupportsTableAndJSON(t *testing.T) {
	events := []model.Event{{Sequence: 7, Timestamp: time.Unix(1, 0), Type: "state_transition", State: model.StateRunning, Message: "ready"}}
	var tableOutput bytes.Buffer
	if err := writeEvents(&tableOutput, events, false); err != nil {
		t.Fatal(err)
	}
	if output := tableOutput.String(); !strings.Contains(output, "Remora Events") || !strings.Contains(output, "RUNNING") {
		t.Fatalf("events table=%s", output)
	}
	var jsonOutput bytes.Buffer
	if err := writeEvents(&jsonOutput, events, true); err != nil {
		t.Fatal(err)
	}
	var decoded []model.Event
	if err := json.Unmarshal(jsonOutput.Bytes(), &decoded); err != nil || len(decoded) != 1 || decoded[0].Sequence != 7 {
		t.Fatalf("events JSON=%s err=%v", jsonOutput.String(), err)
	}
}
