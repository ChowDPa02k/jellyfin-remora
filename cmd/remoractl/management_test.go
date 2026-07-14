package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/control"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

func TestManagementCommandsRoundTrip(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/logs":
			_ = json.NewEncoder(w).Encode(control.LogResponse{Source: "remora", Lines: []string{"line one", "line two"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apikeys":
			_ = json.NewEncoder(w).Encode(control.APIKeysResponse{Keys: []model.APIKey{{ID: "abcdef0123456789", Name: "Kodi"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apikeys":
			_ = json.NewEncoder(w).Encode(model.APIKey{ID: "1234567890abcdef", Name: "Living Room"})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/apikeys/"):
			_ = json.NewEncoder(w).Encode(map[string]bool{"deleted": true})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sessions":
			_ = json.NewEncoder(w).Encode(control.SessionsResponse{Sessions: []model.Session{{ID: "session-12345678", User: "alice", Status: "playing"}}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/stop"):
			_ = json.NewEncoder(w).Encode(map[string]bool{"stopped": true})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/diagnostics":
			_ = json.NewEncoder(w).Encode(control.DiagnosticBundle{Status: model.Status{State: model.StateRunning}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	for _, command := range []func() error{
		func() error { return runLogs(server.Client(), server.URL, []string{"--lines", "2"}, false) },
		func() error { return runAPIKey(server.Client(), server.URL, []string{"list", "--json"}, false) },
		func() error { return runAPIKey(server.Client(), server.URL, []string{"create", "Living Room"}, false) },
		func() error { return runAPIKey(server.Client(), server.URL, []string{"delete", "abcdef01"}, false) },
		func() error { return runSession(server.Client(), server.URL, []string{"list"}, false) },
		func() error { return runSession(server.Client(), server.URL, []string{"stop", "session-"}, false) },
	} {
		if _, err := captureOutput(command); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "diagnostics.json")
	if _, err := captureOutput(func() error { return runDiagnose(server.Client(), server.URL, []string{"--output", output}) }); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("diagnostic file info=%v err=%v", info, err)
	}
	if len(requests) != 7 {
		t.Fatalf("requests=%v", requests)
	}
}

func TestEditExistingConfigValidatesAndAtomicallyReplaces(t *testing.T) {
	source := filepath.Join("..", "..", "sample", "config-darwin.yaml")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	oldEditor := editConfigFile
	editConfigFile = func(_, temporary string) error {
		f, err := os.OpenFile(temporary, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.WriteString(f, "\n# edited by test\n")
		return err
	}
	t.Cleanup(func() { editConfigFile = oldEditor })
	location := control.ConfigResponse{Path: path, SHA256: digest(data)}
	if _, err := captureOutput(func() error { return editExistingConfig(location, "true") }); err != nil {
		t.Fatal(err)
	}
	edited, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(edited), "edited by test") {
		t.Fatalf("edited=%q err=%v", edited, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func captureOutput(fn func() error) (string, error) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = old
	b, readErr := io.ReadAll(r)
	_ = r.Close()
	if runErr != nil {
		return string(b), runErr
	}
	return string(b), readErr
}
