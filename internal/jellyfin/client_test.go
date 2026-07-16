package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
			if info.ServerName == "" {
				t.Fatal("public info fixture omitted server name")
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
			var sessions []sessionInfo
			decode("sessions", &sessions)
			if len(sessions) == 0 || sessions[0].ID == "" || sessions[0].UserName == "" {
				t.Fatalf("sessions = %#v", sessions)
			}
		})
	}
}

func TestSessionsNormalizesPlaybackAndDevice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Sessions" || !strings.Contains(r.Header.Get("Authorization"), `Token="api-key"`) {
			t.Errorf("request = %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode([]sessionInfo{
			{ID: "playing-id", UserName: "alice", Client: "Jellyfin Web", DeviceName: "Chrome", IsActive: true, NowPlayingItem: &struct {
				Name       string `json:"Name"`
				SeriesName string `json:"SeriesName"`
			}{Name: "Pilot", SeriesName: "Example Series"}, TranscodingInfo: json.RawMessage(`{"VideoCodec":"h264"}`)},
			{ID: "idle-id", UserName: "bob", Client: "Findroid", IsActive: true},
			{ID: "paused-id", UserName: "carol", Client: "Jellyfin Media Player", IsActive: true, NowPlayingItem: &struct {
				Name       string `json:"Name"`
				SeriesName string `json:"SeriesName"`
			}{Name: "The Matrix"}, PlayState: &struct {
				IsPaused bool `json:"IsPaused"`
			}{IsPaused: true}},
			{ID: "anonymous", Client: "ignored", IsActive: true},
			{ID: "inactive", UserName: "carol", Client: "ignored"},
		})
	}))
	defer srv.Close()
	sessions, err := New(srv.URL, time.Second).Sessions(context.Background(), "api-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 || sessions[0].Status != "playing" || sessions[0].Device != "Jellyfin Web (Chrome)" || sessions[0].Media != "Example Series — Pilot" || !sessions[0].Transcoding {
		t.Fatalf("sessions = %#v", sessions)
	}
	if sessions[1].Status != "idle" {
		t.Fatalf("idle session = %#v", sessions[1])
	}
	if sessions[2].Status != "paused" || sessions[2].Media != "The Matrix" {
		t.Fatalf("paused session = %#v", sessions[2])
	}
}

func TestAPIKeyManagementAndSessionStopRequests(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		if !strings.Contains(r.Header.Get("Authorization"), `Token="admin-token"`) {
			t.Errorf("missing token: %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/Auth/Keys":
			_ = json.NewEncoder(w).Encode(authenticationInfoQuery{Items: []AuthenticationInfo{{AccessToken: "key", AppName: "Kodi", IsActive: true}}})
		case r.Method == http.MethodPost && r.URL.Path == "/Auth/Keys":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/Auth/Keys/key":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/Sessions/session-id/Playing/Stop":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client := New(srv.URL, time.Second)
	keys, err := client.APIKeys(context.Background(), "admin-token")
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	if err := client.CreateAPIKey(context.Background(), "admin-token", "Living Room"); err != nil {
		t.Fatal(err)
	}
	if err := client.RevokeAPIKey(context.Background(), "admin-token", "key"); err != nil {
		t.Fatal(err)
	}
	if err := client.StopSession(context.Background(), "admin-token", "session-id"); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /Auth/Keys", "POST /Auth/Keys?app=Living+Room", "DELETE /Auth/Keys/key", "POST /Sessions/session-id/Playing/Stop"}
	if strings.Join(requests, "|") != strings.Join(want, "|") {
		t.Fatalf("requests=%v, want %v", requests, want)
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
	wantPaths := []string{"/Startup/User", "/Startup/Configuration", "/Localization/Options", "/Localization/Cultures", "/Localization/Countries", "/Startup/Configuration", "/Startup/User", "/Startup/RemoteAccess", "/Startup/Complete"}
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
		if r.Method == http.MethodGet {
			switch r.URL.Path {
			case "/Startup/Configuration":
				_ = json.NewEncoder(w).Encode(map[string]any{"ServerName": "Default", "UICulture": "en-US", "MetadataCountryCode": "GB", "PreferredMetadataLanguage": "en"})
			case "/Localization/Options":
				_ = json.NewEncoder(w).Encode([]localizationOption{{Name: "English", Value: "en-US"}})
			case "/Localization/Cultures":
				_ = json.NewEncoder(w).Encode([]cultureOption{{DisplayName: "Chinese", TwoLetterISOLanguageName: "zh"}})
			case "/Localization/Countries":
				_ = json.NewEncoder(w).Encode([]countryOption{{DisplayName: "United States", TwoLetterISORegionName: "US"}})
			}
			return
		}
		if r.URL.Path == "/Startup/Configuration" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["UICulture"] != "en-US" || body["MetadataCountryCode"] != "US" || body["PreferredMetadataLanguage"] != "zh" || body["ServerName"] != "Jellyfin" {
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

func TestStartupConfigurationUsesJellyfinWebSelectionLabels(t *testing.T) {
	display := []localizationOption{{Name: "العربية", Value: "ar"}, {Name: "한국어", Value: "ko"}, {Name: "Deutsch", Value: "de"}}
	languages := []cultureOption{{DisplayName: "Arabic", TwoLetterISOLanguageName: "ar"}, {DisplayName: "Korean", TwoLetterISOLanguageName: "ko"}, {DisplayName: "German", TwoLetterISOLanguageName: "de"}}
	regions := []countryOption{{DisplayName: "Saudi Arabia", TwoLetterISORegionName: "SA"}, {DisplayName: "Korea", TwoLetterISORegionName: "KR"}, {DisplayName: "Germany", TwoLetterISORegionName: "DE"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Startup/Configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"UICulture": "en-US", "PreferredMetadataLanguage": "en", "MetadataCountryCode": "US"})
		case "/Localization/Options":
			_ = json.NewEncoder(w).Encode(display)
		case "/Localization/Cultures":
			_ = json.NewEncoder(w).Encode(languages)
		case "/Localization/Countries":
			_ = json.NewEncoder(w).Encode(regions)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	for _, test := range []struct {
		name, display, language, region, wantDisplay, wantLanguage, wantRegion string
	}{
		{name: "Arabic", display: "العربية", language: "Arabic", region: "Saudi Arabia", wantDisplay: "ar", wantLanguage: "ar", wantRegion: "SA"},
		{name: "Korean", display: "한국어", language: "Korean", region: "Korea", wantDisplay: "ko", wantLanguage: "ko", wantRegion: "KR"},
		{name: "German", display: "Deutsch", language: "German", region: "Germany", wantDisplay: "de", wantLanguage: "de", wantRegion: "DE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.InitConfig{DisplayLanguage: test.display, PreferredMetadataLanguage: test.language, PreferredMetadataRegion: test.region}
			got, err := New(srv.URL, time.Second).startupConfiguration(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got["UICulture"] != test.wantDisplay || got["PreferredMetadataLanguage"] != test.wantLanguage || got["MetadataCountryCode"] != test.wantRegion {
				t.Fatalf("resolved configuration = %#v", got)
			}
		})
	}
}

func TestStartupConfigurationRejectsInternalSelectionValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Startup/Configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case "/Localization/Options":
			_ = json.NewEncoder(w).Encode([]localizationOption{{Name: "Deutsch", Value: "de"}})
		case "/Localization/Cultures", "/Localization/Countries":
			_ = json.NewEncoder(w).Encode([]any{})
		}
	}))
	defer srv.Close()

	_, err := New(srv.URL, time.Second).startupConfiguration(context.Background(), config.InitConfig{DisplayLanguage: "de"})
	if err == nil || !strings.Contains(err.Error(), "not a label offered") {
		t.Fatalf("internal selection value error = %v", err)
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

func TestUpdateServerNamePreservesSystemConfiguration(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), `Token="admin-token"`) {
			t.Errorf("missing administrator token: %q", r.Header.Get("Authorization"))
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"ServerName": "", "UICulture": "ko", "PluginRepositories": []any{"preserve"}})
		case http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&posted)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	if err := New(srv.URL, time.Second).UpdateServerName(context.Background(), "admin-token", "Remora Kickstart"); err != nil {
		t.Fatal(err)
	}
	if posted["ServerName"] != "Remora Kickstart" || posted["UICulture"] != "ko" || posted["PluginRepositories"] == nil {
		t.Fatalf("posted configuration was not preserved: %#v", posted)
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

func TestShutdownUsesAuthenticatedSystemEndpoint(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost || r.URL.Path != "/System/Shutdown" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Authorization"), `Token="api-key"`) {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := New(srv.URL, time.Second).Shutdown(context.Background(), "api-key"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("shutdown endpoint was not called")
	}
}

func TestEnsureWatchdogUserCreatesMissingUserAndReusesSession(t *testing.T) {
	created := false
	authentications := 0
	identityChecks := 0
	var watchdogIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deviceID := authorizationDeviceID(r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/Users/AuthenticateByName":
			authentications++
			watchdogIDs = append(watchdogIDs, deviceID)
			if !created {
				http.Error(w, "unknown user", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(AuthenticationResult{AccessToken: "watchdog-token"})
		case "/Users/New":
			if !strings.Contains(r.Header.Get("Authorization"), `Token="admin-token"`) {
				t.Errorf("missing administrator token")
			}
			if deviceID != adminDeviceID {
				t.Errorf("user creation device ID = %q, want %q", deviceID, adminDeviceID)
			}
			created = true
			_ = json.NewEncoder(w).Encode(map[string]string{"Name": "remora"})
		case "/Users/Me":
			identityChecks++
			watchdogIDs = append(watchdogIDs, deviceID)
			if !strings.Contains(r.Header.Get("Authorization"), `Token="watchdog-token"`) {
				t.Errorf("missing watchdog token")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Name": "remora"})
		case "/Sessions/Logout":
			t.Error("persistent watchdog session must not log out after a health check")
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := config.UserLoginWatchdogConfig{Enabled: true, User: "remora", Password: "secret"}
	client := New(srv.URL, time.Second)
	if err := client.EnsureWatchdogUser(context.Background(), "admin-token", cfg); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureWatchdogUser(context.Background(), "admin-token", cfg); err != nil {
		t.Fatal(err)
	}
	if !created || authentications != 2 || identityChecks != 2 {
		t.Fatalf("created=%v authentications=%d identityChecks=%d", created, authentications, identityChecks)
	}
	if len(watchdogIDs) != 4 {
		t.Fatalf("watchdog request device IDs = %v", watchdogIDs)
	}
	for _, deviceID := range watchdogIDs {
		if deviceID != watchdogIDs[0] {
			t.Fatalf("watchdog request device IDs differ: %v", watchdogIDs)
		}
	}
	if watchdogIDs[0] != watchdogDeviceID || watchdogIDs[0] == defaultDeviceID || watchdogIDs[0] == adminDeviceID {
		t.Fatalf("watchdog device ID is not isolated: %q", watchdogIDs[0])
	}
}

func TestAuthenticateUsesIsolatedAdminDevice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := authorizationDeviceID(r.Header.Get("Authorization")); got != adminDeviceID {
			t.Errorf("device ID = %q, want %q", got, adminDeviceID)
		}
		_ = json.NewEncoder(w).Encode(AuthenticationResult{AccessToken: "admin-token"})
	}))
	defer srv.Close()
	if _, err := New(srv.URL, time.Second).Authenticate(context.Background(), "admin", "secret"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureWatchdogReauthenticatesOnlyAfterTokenRejection(t *testing.T) {
	authentications := 0
	rejectFirstToken := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/AuthenticateByName":
			authentications++
			_ = json.NewEncoder(w).Encode(AuthenticationResult{AccessToken: fmt.Sprintf("watchdog-token-%d", authentications)})
		case "/Users/Me":
			if rejectFirstToken && strings.Contains(r.Header.Get("Authorization"), `Token="watchdog-token-1"`) {
				http.Error(w, "token revoked", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Name": "remora"})
		case "/Sessions/Logout":
			t.Error("persistent watchdog session must not log out")
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := config.UserLoginWatchdogConfig{Enabled: true, User: "remora", Password: "secret"}
	client := New(srv.URL, time.Second)
	if err := client.EnsureWatchdogUser(context.Background(), "admin-token", cfg); err != nil {
		t.Fatal(err)
	}
	rejectFirstToken = true
	if err := client.EnsureWatchdogUser(context.Background(), "admin-token", cfg); err != nil {
		t.Fatal(err)
	}
	if authentications != 2 {
		t.Fatalf("authentications=%d, want 2", authentications)
	}
}

func TestEnsureWatchdogKeepsSessionAcrossTransientIdentityFailure(t *testing.T) {
	authentications := 0
	failIdentity := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/AuthenticateByName":
			authentications++
			_ = json.NewEncoder(w).Encode(AuthenticationResult{AccessToken: "watchdog-token"})
		case "/Users/Me":
			if failIdentity {
				failIdentity = false
				http.Error(w, "server busy", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Name": "remora"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := config.UserLoginWatchdogConfig{Enabled: true, User: "remora", Password: "secret"}
	client := New(srv.URL, time.Second)
	if err := client.EnsureWatchdogUser(context.Background(), "admin-token", cfg); err != nil {
		t.Fatal(err)
	}
	failIdentity = true
	err := client.EnsureWatchdogUser(context.Background(), "admin-token", cfg)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("identity check failure was not propagated: %v", err)
	}
	if err := client.EnsureWatchdogUser(context.Background(), "admin-token", cfg); err != nil {
		t.Fatal(err)
	}
	if authentications != 1 {
		t.Fatalf("transient failure caused %d authentications, want 1", authentications)
	}
}

func authorizationDeviceID(header string) string {
	const marker = `DeviceId="`
	start := strings.Index(header, marker)
	if start < 0 {
		return ""
	}
	value := header[start+len(marker):]
	end := strings.IndexByte(value, '"')
	if end < 0 {
		return ""
	}
	return value[:end]
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
