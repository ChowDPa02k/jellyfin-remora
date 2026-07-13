package control

import (
	"context"
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
	action supervisor.Action
	force  bool
}

func (f *fakeController) Status() model.Status { return f.status }
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
