package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
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
		wantWidth := displayWidth(lines[0])
		for _, line := range lines[1:] {
			if got := displayWidth(line); got != wantWidth {
				t.Fatalf("misaligned table width=%d want=%d line=%q\n%s", got, wantWidth, line, block)
			}
		}
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
