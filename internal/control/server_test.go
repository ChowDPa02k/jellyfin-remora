package control

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/supervisor"
)

type fakeController struct {
	status model.Status
	events []model.Event
	action supervisor.Action
	force  bool
}

func (f *fakeController) Status() model.Status { return f.status }
func (f *fakeController) Events(limit int) []model.Event {
	if limit > len(f.events) {
		limit = len(f.events)
	}
	return f.events[len(f.events)-limit:]
}
func (f *fakeController) Submit(_ context.Context, a supervisor.Action, force bool) error {
	f.action = a
	f.force = force
	return nil
}

func TestStatusEndpoint(t *testing.T) {
	f := &fakeController{status: model.Status{State: model.StateRunning, PID: 42}}
	s := New(&config.Config{}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Remora-API-Version") != "1" || w.Header().Get("X-Remora-Operation-ID") == "" {
		t.Fatalf("missing API metadata headers: %v", w.Header())
	}
}
func TestForceStopEndpoint(t *testing.T) {
	f := &fakeController{}
	s := New(&config.Config{}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := httptest.NewRequest(http.MethodPost, "/v1/stop?force=true", nil)
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	if w.Code != http.StatusAccepted || f.action != supervisor.ActionStop || !f.force {
		t.Fatalf("code=%d action=%s force=%t", w.Code, f.action, f.force)
	}
}
func TestStartRejectedWhileStorageFenced(t *testing.T) {
	f := &fakeController{status: model.Status{State: model.StateStorageFenced}}
	s := New(&config.Config{}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := httptest.NewRequest(http.MethodPost, "/v1/start", nil)
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("code=%d", w.Code)
	}
	var response ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "storage_fenced" || response.Error.OperationID == "" {
		t.Fatalf("error response=%+v", response)
	}
}

func TestEventsEndpointIsBoundedAndValidated(t *testing.T) {
	f := &fakeController{events: []model.Event{{Sequence: 1}, {Sequence: 2}, {Sequence: 3}}}
	s := New(&config.Config{}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, test := range []struct {
		url      string
		wantCode int
		want     int
	}{
		{url: "/v1/events?limit=2", wantCode: http.StatusOK, want: 2},
		{url: "/v1/events?limit=0", wantCode: http.StatusBadRequest},
		{url: "/v1/events?limit=257", wantCode: http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, test.url, nil))
		if w.Code != test.wantCode {
			t.Fatalf("%s code=%d body=%s", test.url, w.Code, w.Body.String())
		}
		if test.want > 0 {
			var response EventResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Events) != test.want || response.Events[0].Sequence != 2 {
				t.Fatalf("events=%+v", response.Events)
			}
		}
	}
}

func TestAPIMethodAndForceValidationUseStructuredErrors(t *testing.T) {
	f := &fakeController{}
	s := New(&config.Config{}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, test := range []struct {
		method   string
		url      string
		wantCode int
		wantAPI  string
	}{
		{method: http.MethodPost, url: "/v1/status", wantCode: http.StatusMethodNotAllowed, wantAPI: "method_not_allowed"},
		{method: http.MethodPost, url: "/v1/stop?force=immediately", wantCode: http.StatusBadRequest, wantAPI: "invalid_argument"},
	} {
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, httptest.NewRequest(test.method, test.url, nil))
		if w.Code != test.wantCode {
			t.Fatalf("%s %s code=%d body=%s", test.method, test.url, w.Code, w.Body.String())
		}
		var response ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error.Code != test.wantAPI || response.Error.OperationID == "" {
			t.Fatalf("error response=%+v", response)
		}
	}
}

func TestSafeRemoveSocketRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	if err := os.WriteFile(path, []byte("do not delete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := safeRemoveSocket(path); err == nil {
		t.Fatal("expected regular-file rejection")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("regular file was removed: %v", err)
	}
}
