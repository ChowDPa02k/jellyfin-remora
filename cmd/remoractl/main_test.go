package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

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
	if err := wait(srv.Client(), srv.URL, "start", 0); err != nil {
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
	if err := wait(srv.Client(), srv.URL, "start", 0); err != nil {
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
	if err := wait(srv.Client(), srv.URL, "restart", 42); err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 3 {
		t.Fatalf("restart returned before PID changed: calls=%d", calls.Load())
	}
}
