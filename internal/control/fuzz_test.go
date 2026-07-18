package control

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/contract"
)

func FuzzAPIJSONBody(f *testing.F) {
	f.Add([]byte(`{"name":"fuzz-key"}`))
	f.Add([]byte(`{"name":"x","unknown":true}`))
	f.Add(bytes.Repeat([]byte("x"), 16*1024+1))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 1<<20 {
			t.Skip()
		}
		missingLog := t.TempDir() + "/missing.log"
		server := NewWithOptions(&config.Config{}, &fakeController{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{LogPath: missingLog, JellyfinLogPath: missingLog})
		request := httptest.NewRequest(http.MethodPost, "/v1/apikeys", bytes.NewReader(body))
		response := httptest.NewRecorder()
		server.handler().ServeHTTP(response, request)
		if response.Code >= 500 || response.Code < 200 {
			t.Fatalf("unexpected status %d for %d-byte body", response.Code, len(body))
		}
		if response.Header().Get(contract.APIHeaderVersion) == "" || response.Header().Get(contract.APIHeaderOperationID) == "" {
			t.Fatal("API metadata headers are missing")
		}
		var decoded any
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("API emitted invalid JSON: %v", err)
		}
	})
}

func FuzzAPIQueryParsing(f *testing.F) {
	f.Add("200", "false", "remora")
	f.Add("-1", "maybe", "../jellyfin")
	f.Fuzz(func(t *testing.T, lines, follow, source string) {
		if len(lines)+len(follow)+len(source) > 16*1024 {
			t.Skip()
		}
		missingLog := t.TempDir() + "/missing.log"
		server := NewWithOptions(&config.Config{}, &fakeController{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{LogPath: missingLog, JellyfinLogPath: missingLog})
		values := url.Values{"lines": {lines}, "follow": {follow}, "source": {source}}
		request := httptest.NewRequest(http.MethodGet, "/v1/logs?"+values.Encode(), nil)
		response := httptest.NewRecorder()
		server.handler().ServeHTTP(response, request)
		if response.Code >= 500 || response.Code < 200 {
			t.Fatalf("unexpected status %d", response.Code)
		}
		if response.Header().Get(contract.APIHeaderOperationID) == "" {
			t.Fatal("operation ID is missing")
		}
	})
}
