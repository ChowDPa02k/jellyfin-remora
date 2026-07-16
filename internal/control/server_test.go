package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/contract"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/supervisor"
)

type fakeController struct {
	status   model.Status
	events   []model.Event
	keys     []model.APIKey
	sessions []model.Session
	action   supervisor.Action
	force    bool
}

type cancellationController struct{ *fakeController }

func (c *cancellationController) Submit(ctx context.Context, _ supervisor.Action, _ bool) error {
	<-ctx.Done()
	return ctx.Err()
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
func (f *fakeController) APIKeys(context.Context) ([]model.APIKey, error) {
	return append([]model.APIKey(nil), f.keys...), nil
}
func (f *fakeController) CreateAPIKey(_ context.Context, name string) (model.APIKey, error) {
	key := model.APIKey{ID: "1234567890abcdef", Name: name, Active: true}
	f.keys = append(f.keys, key)
	return key, nil
}
func (f *fakeController) DeleteAPIKey(_ context.Context, id string) error {
	f.keys = nil
	return nil
}
func (f *fakeController) Sessions(context.Context) ([]model.Session, error) {
	return append([]model.Session(nil), f.sessions...), nil
}
func (f *fakeController) StopSession(_ context.Context, id string) error { return nil }

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

func TestFrozenAPIOperationsRemainRegistered(t *testing.T) {
	f := &fakeController{}
	s := New(&config.Config{}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, operation := range contract.APIOperations {
		path := strings.ReplaceAll(strings.ReplaceAll(operation.Path, "{id}", "fixture-id"), "{session}", "fixture-session")
		body := io.Reader(nil)
		if operation.Method == http.MethodPost && operation.Path == "/v1/apikeys" {
			body = strings.NewReader(`{"name":"compatibility-test"}`)
		}
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, httptest.NewRequest(operation.Method, path, body))
		if w.Code == http.StatusMethodNotAllowed {
			t.Fatalf("frozen operation is no longer registered: %s %s", operation.Method, operation.Path)
		}
		if w.Code == http.StatusNotFound {
			var response ErrorResponse
			_ = json.Unmarshal(w.Body.Bytes(), &response)
			if response.Error.Code == "not_found" {
				t.Fatalf("frozen operation is no longer registered: %s %s", operation.Method, operation.Path)
			}
		}
		if got := w.Header().Get(contract.APIHeaderVersion); got != "1" {
			t.Fatalf("%s %s version header=%q", operation.Method, operation.Path, got)
		}
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

func TestRestartRejectedWhileDatabaseDamageIsLatched(t *testing.T) {
	f := &fakeController{status: model.Status{State: model.StateDatabaseDamaged, Database: model.DatabaseResult{Damaged: true}}}
	s := New(&config.Config{}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/restart", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "database_damaged" {
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

func TestManagementLogConfigDiagnosticKeyAndSessionEndpoints(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "remora.log")
	jellyfinLogPath := filepath.Join(root, "jellyfin-console.log")
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(logPath, []byte("Logging out access token secret-session password=secret-password\none\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jellyfinLogPath, []byte("jellyfin raw line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("password: must-not-leak\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeController{keys: []model.APIKey{{ID: "abcdef0123456789", Name: "Kodi"}}, sessions: []model.Session{{ID: "session-12345678", User: "alice", Status: "playing"}}}
	cfg := &config.Config{ConfigVersion: 2, RESTAPI: config.RESTAPIConfig{Listen: "127.0.0.1", Port: 8095, UnixSocket: filepath.Join(root, "control.sock")}, Remora: config.RemoraConfig{DataDir: root}, Jellyfin: config.JellyfinConfig{Path: "/Applications/Jellyfin", LogDir: root}}
	s := NewWithOptions(cfg, f, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{ConfigPath: configPath, LogPath: logPath, JellyfinLogPath: jellyfinLogPath})

	for _, request := range []struct {
		method, path string
		body         string
		code         int
		contains     string
	}{
		{http.MethodGet, "/v1/logs?lines=2", "", http.StatusOK, `"two"`},
		{http.MethodGet, "/v1/logs?source=jellyfin&lines=2", "", http.StatusOK, `"jellyfin raw line"`},
		{http.MethodGet, "/v1/config", "", http.StatusOK, `"sha256"`},
		{http.MethodGet, "/v1/diagnostics", "", http.StatusOK, `"generated_at"`},
		{http.MethodGet, "/v1/apikeys", "", http.StatusOK, `"Kodi"`},
		{http.MethodPost, "/v1/apikeys", `{"name":"Living Room"}`, http.StatusCreated, `"Living Room"`},
		{http.MethodDelete, "/v1/apikeys/abcdef01", "", http.StatusOK, `"deleted"`},
		{http.MethodGet, "/v1/sessions", "", http.StatusOK, `"alice"`},
		{http.MethodPost, "/v1/sessions/session-/stop", "", http.StatusOK, `"stopped"`},
		{http.MethodPost, "/v1/apikeys", `{"name":"x","unknown":true}`, http.StatusBadRequest, `"invalid_argument"`},
	} {
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, httptest.NewRequest(request.method, request.path, strings.NewReader(request.body)))
		if w.Code != request.code || !strings.Contains(w.Body.String(), request.contains) {
			t.Fatalf("%s %s code=%d body=%s", request.method, request.path, w.Code, w.Body.String())
		}
		if request.path == "/v1/diagnostics" && (strings.Contains(w.Body.String(), "must-not-leak") || strings.Contains(w.Body.String(), "secret-session") || strings.Contains(w.Body.String(), "secret-password") || !strings.Contains(w.Body.String(), "[REDACTED]")) {
			t.Fatal("diagnostics leaked configuration or log credentials")
		}
	}
}

func TestFollowLogStreamsRawANSIAndAppendedContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "jellyfin-console.log")
	if err := os.WriteFile(path, []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewWithOptions(&config.Config{}, &fakeController{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{JellyfinLogPath: path})
	server := httptest.NewServer(s.handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/logs?source=jellyfin&lines=1&follow=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "initial\n" {
		t.Fatalf("initial line=%q err=%v", line, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(f, "\x1b[32mcolored\x1b[0m\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "\x1b[32mcolored\x1b[0m\n" {
		t.Fatalf("followed line=%q err=%v", line, err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after rotation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "after rotation\n" {
		t.Fatalf("rotated line=%q err=%v", line, err)
	}
}

func TestTailLinesIsBoundedAndRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "remora.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, truncated, err := tailLines(path, 2)
	if err != nil || !truncated || strings.Join(lines, ",") != "two,three" {
		t.Fatalf("lines=%v truncated=%t err=%v", lines, truncated, err)
	}
	link := filepath.Join(root, "linked.log")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tailLines(link, 2); err == nil {
		t.Fatal("symlink log read succeeded")
	}
}

func TestConcurrentRequestsHaveUniqueOperationIDsAndLegacyStatusCompatibility(t *testing.T) {
	f := &fakeController{status: model.Status{State: model.StateRunning, PID: 42}}
	s := New(&config.Config{}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	const requests = 64
	ids := make(chan string, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			s.handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
			if w.Code != http.StatusOK {
				t.Errorf("status code=%d", w.Code)
			}
			var legacy struct {
				State model.State `json:"state"`
				PID   int         `json:"pid"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &legacy); err != nil || legacy.PID != 42 {
				t.Errorf("legacy decode=%+v err=%v", legacy, err)
			}
			ids <- w.Header().Get("X-Remora-Operation-ID")
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if id == "" || seen[id] {
			t.Fatalf("invalid or duplicate operation ID %q", id)
		}
		seen[id] = true
	}
}

func TestLegacyClientCanControlNewDaemonAndDecodeStatus(t *testing.T) {
	f := &fakeController{status: model.Status{State: model.StateRunning, PID: 77}}
	s := New(&config.Config{}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/restart", nil))
	var legacy struct {
		State model.State `json:"state"`
		PID   int         `json:"pid"`
	}
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &legacy) != nil || legacy.PID != 77 || f.action != supervisor.ActionRestart {
		t.Fatalf("code=%d legacy=%+v action=%s body=%s", w.Code, legacy, f.action, w.Body.String())
	}
}

func TestManagedHTTPServerBoundsSlowClients(t *testing.T) {
	server := managedHTTPServer("", http.NotFoundHandler())
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.WriteTimeout <= 0 || server.IdleTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatalf("unbounded HTTP server: %+v", server)
	}
}

func TestSlowHeaderClientIsDisconnected(t *testing.T) {
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.Config.ReadHeaderTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()
	connection, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\nHost:"); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 256)
	if _, err := connection.Read(buffer); err != nil {
		if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() {
			t.Fatal("slow header connection survived beyond the server deadline")
		}
	}
}

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	f := &fakeController{}
	s := New(&config.Config{}, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := bytes.Repeat([]byte("x"), 17*1024)
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/apikeys", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_argument") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCanceledMutationDoesNotOutliveRequest(t *testing.T) {
	controller := &cancellationController{fakeController: &fakeController{}}
	s := New(&config.Config{}, controller, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/restart", nil).WithContext(ctx))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "operation_rejected") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}
