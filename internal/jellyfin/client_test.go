package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func TestSupportedReleaseContractFixtures(t *testing.T) {
	for _, release := range []struct {
		dir, versionPrefix string
	}{{"v10.11", "10.11."}, {"v12", "12."}} {
		t.Run(release.dir, func(t *testing.T) {
			decode := func(name string, out any) {
				t.Helper()
				data, err := os.ReadFile(filepath.Join("testdata", release.dir, name+".json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, out); err != nil {
					t.Fatalf("decode %s: %v", name, err)
				}
			}
			var info PublicInfo
			decode("public-info", &info)
			if !strings.HasPrefix(info.Version, release.versionPrefix) || info.StartupWizardCompleted == nil || !*info.StartupWizardCompleted {
				t.Fatalf("public info = %#v", info)
			}
			var startup StartupUser
			decode("startup-user", &startup)
			if startup.Name == "" {
				t.Fatal("startup fixture omitted bootstrap user")
			}
			var auth AuthenticationResult
			decode("authentication-result", &auth)
			if auth.AccessToken == "" || auth.ServerID == "" || auth.User["Id"] == nil {
				t.Fatalf("authentication result = %#v", auth)
			}
			var keys authenticationInfoQuery
			decode("auth-keys", &keys)
			if len(keys.Items) != 1 || keys.Items[0].AppName != "Jellyfin Remora" || keys.Items[0].AccessToken == "" {
				t.Fatalf("API keys = %#v", keys)
			}
		})
	}
}

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	got := New(srv.URL, time.Second).Health(context.Background())
	if !got.Healthy || got.StatusCode != 200 {
		t.Fatalf("health=%+v", got)
	}
}
func TestHealthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad", http.StatusServiceUnavailable) }))
	defer srv.Close()
	got := New(srv.URL, time.Second).Health(context.Background())
	if got.Healthy || got.StatusCode != 503 {
		t.Fatalf("health=%+v", got)
	}
}

func TestCompleteStartupSequence(t *testing.T) {
	wantPaths := []string{"/Startup/User", "/Startup/Configuration", "/Startup/User", "/Startup/RemoteAccess", "/Startup/Complete"}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if !strings.Contains(r.Header.Get("Authorization"), `Client="Jellyfin%20Remora"`) {
			t.Errorf("unexpected request: %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		if r.Method == http.MethodGet && r.URL.Path == "/Startup/User" {
			_ = json.NewEncoder(w).Encode(StartupUser{Name: "mac-user"})
			return
		}
		if r.URL.Path == "/Startup/Configuration" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["UICulture"] != "en-US" || body["MetadataCountryCode"] != "US" || body["PreferredMetadataLanguage"] != "zh-CN" {
				t.Errorf("mapped configuration=%v", body)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cfg := config.InitConfig{ServerName: "Jellyfin", DisplayLanguage: "English", User: "admin", Password: "secret", PreferredMetadataLanguage: "Chinese", PreferredMetadataRegion: "United States", AllowRemoteConnections: true}
	bootstrap, err := New(srv.URL, time.Second).CompleteStartup(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap != "mac-user" {
		t.Fatalf("bootstrap=%q", bootstrap)
	}
	if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("paths=%v", paths)
	}
}

func TestUpdateUsernamePreservesUserDocument(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Users" || r.URL.Query().Get("userId") != "user-id" {
			t.Errorf("url=%s", r.URL.String())
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	user := map[string]any{"Id": "user-id", "Name": "mac-user", "Policy": map[string]any{"IsAdministrator": true}}
	if err := New(srv.URL, time.Second).UpdateUsername(context.Background(), "token", user, "admin"); err != nil {
		t.Fatal(err)
	}
	if got["Name"] != "admin" || got["Policy"] == nil {
		t.Fatalf("user=%v", got)
	}
}

func TestEnsureAPIKeyCreatesAndReturnsKey(t *testing.T) {
	created := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), `Token="admin-token"`) {
			t.Errorf("missing administrator token: %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/Auth/Keys":
			items := []AuthenticationInfo{}
			if created {
				// Jellyfin 12 reports application API keys as inactive until use.
				items = append(items, AuthenticationInfo{AccessToken: "remora-key", AppName: "Jellyfin Remora", IsActive: false})
			}
			_ = json.NewEncoder(w).Encode(authenticationInfoQuery{Items: items})
		case r.Method == http.MethodPost && r.URL.Path == "/Auth/Keys":
			if r.URL.Query().Get("app") != "Jellyfin Remora" {
				t.Errorf("app=%q", r.URL.Query().Get("app"))
			}
			created = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	key, err := New(srv.URL, time.Second).EnsureAPIKey(context.Background(), "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	if key != "remora-key" || !created {
		t.Fatalf("key=%q created=%v", key, created)
	}
}

func TestValidateAPIKeyRejectsRevokedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Authorization"), `Token="revoked"`) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(authenticationInfoQuery{})
	}))
	defer srv.Close()
	err := New(srv.URL, time.Second).ValidateAPIKey(context.Background(), "revoked")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error=%v", err)
	}
}

func TestEnsureWatchdogUserCreatesMissingUserAndLogsIn(t *testing.T) {
	created := false
	logouts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/AuthenticateByName":
			if !created {
				http.Error(w, "unknown user", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(AuthenticationResult{AccessToken: "watchdog-token"})
		case "/Users/New":
			if !strings.Contains(r.Header.Get("Authorization"), `Token="admin-token"`) {
				t.Errorf("missing administrator token")
			}
			created = true
			_ = json.NewEncoder(w).Encode(map[string]string{"Name": "remora"})
		case "/Users/Me":
			if !strings.Contains(r.Header.Get("Authorization"), `Token="watchdog-token"`) {
				t.Errorf("missing watchdog token")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Name": "remora"})
		case "/Sessions/Logout":
			logouts++
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := config.UserLoginWatchdogConfig{Enabled: true, User: "remora", Password: "secret"}
	if err := New(srv.URL, time.Second).EnsureWatchdogUser(context.Background(), "admin-token", cfg); err != nil {
		t.Fatal(err)
	}
	if !created || logouts != 1 {
		t.Fatalf("created=%v logouts=%d", created, logouts)
	}
}

func TestEnsureWatchdogWrongPasswordFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/AuthenticateByName":
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
		case "/Users/New":
			http.Error(w, "user already exists", http.StatusConflict)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := config.UserLoginWatchdogConfig{Enabled: true, User: "remora", Password: "wrong"}
	err := New(srv.URL, time.Second).EnsureWatchdogUser(context.Background(), "admin-token", cfg)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("watchdog failure was not propagated: %v", err)
	}
}
