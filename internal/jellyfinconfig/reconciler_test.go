package jellyfinconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func optional[T any](value T) config.Optional[T] {
	return config.Optional[T]{Set: true, Value: value}
}

func nullOptional[T any]() config.Optional[T] {
	return config.Optional[T]{Set: true, Null: true}
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
}

func fixtureConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "system.xml"), `<?xml version="1.0" encoding="utf-8"?>
<ServerConfiguration><IsStartupWizardCompleted>true</IsStartupWizardCompleted><ServerName>Dashboard</ServerName><KeepMe>owned-by-jellyfin</KeepMe><CachePath>/old/cache</CachePath><MetadataPath>/old/metadata</MetadataPath><LibraryScanFanoutConcurrency>0</LibraryScanFanoutConcurrency><ParallelImageEncodingLimit>4</ParallelImageEncodingLimit></ServerConfiguration>`)
	writeFixture(t, filepath.Join(dir, "encoding.xml"), `<?xml version="1.0" encoding="utf-8"?>
<EncodingOptions><EncodingThreadCount>-1</EncodingThreadCount><EnableFallbackFont>false</EnableFallbackFont><FallbackFontPath></FallbackFontPath><TranscodingTempPath></TranscodingTempPath></EncodingOptions>`)
	writeFixture(t, filepath.Join(dir, "network.xml"), `<?xml version="1.0" encoding="utf-8"?>
<NetworkConfiguration><InternalHttpPort>8096</InternalHttpPort><InternalHttpsPort>8920</InternalHttpsPort><EnableHttps>false</EnableHttps><BaseUrl></BaseUrl><LocalNetworkAddresses><string>127.0.0.1</string></LocalNetworkAddresses><EnableIPv4>true</EnableIPv4><EnableIPv6>false</EnableIPv6><KeepNetwork>true</KeepNetwork></NetworkConfiguration>`)
	return &config.Config{Jellyfin: config.JellyfinConfig{
		ConfigDir: dir,
		General: config.GeneralConfig{
			Settings:    config.GeneralSettings{ServerName: optional("Managed")},
			Paths:       config.GeneralPaths{CachePath: nullOptional[string](), MetadataPath: optional("default")},
			Performance: config.GeneralPerformance{ParallelLibraryScanTasksLimit: optional(1), ParallelImageEncodingLimit: nullOptional[int]()},
		},
		Branding: config.BrandingConfig{LoginDisclaimer: optional("Welcome & enjoy")},
		Playback: config.PlaybackConfig{Transcoding: config.TranscodingConfig{EnableFallbackFonts: optional(true)}},
		Networking: config.NetworkingConfig{
			ServerAddressSettings: config.ServerAddressSettings{LocalHTTPPort: 8097, LocalHTTPPortConfigured: true, BindToLocalNetworkAddress: config.OptionalStrings{Set: true, Null: true}},
			IPProtocols:           config.IPProtocols{EnableIPv6: optional(true)},
		},
	}}
}

func TestReconcilePreservesUnmanagedFieldsBacksUpAndIsIdempotent(t *testing.T) {
	cfg := fixtureConfig(t)
	systemPath := filepath.Join(cfg.Jellyfin.ConfigDir, "system.xml")
	originalSystem, err := os.ReadFile(systemPath)
	if err != nil {
		t.Fatal(err)
	}
	reconciler := New(cfg)
	result, err := reconciler.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ChangedFiles) != 4 || len(result.BackupFiles) != 3 {
		t.Fatalf("result = %#v", result)
	}
	backup, err := os.ReadFile(systemPath + ".remora.bak")
	if err != nil || string(backup) != string(originalSystem) {
		t.Fatalf("system backup does not match original: %v", err)
	}
	info, err := os.Stat(systemPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("system mode = %v", info.Mode().Perm())
	}
	assertScalar(t, filepath.Join(cfg.Jellyfin.ConfigDir, "system.xml"), "ServerConfiguration", "ServerName", "Managed")
	assertScalar(t, filepath.Join(cfg.Jellyfin.ConfigDir, "system.xml"), "ServerConfiguration", "CachePath", "")
	assertScalar(t, filepath.Join(cfg.Jellyfin.ConfigDir, "system.xml"), "ServerConfiguration", "MetadataPath", "")
	assertScalar(t, filepath.Join(cfg.Jellyfin.ConfigDir, "system.xml"), "ServerConfiguration", "LibraryScanFanoutConcurrency", "1")
	assertScalar(t, filepath.Join(cfg.Jellyfin.ConfigDir, "system.xml"), "ServerConfiguration", "KeepMe", "owned-by-jellyfin")
	assertScalar(t, filepath.Join(cfg.Jellyfin.ConfigDir, "branding.xml"), "BrandingOptions", "LoginDisclaimer", "Welcome & enjoy")
	assertScalar(t, filepath.Join(cfg.Jellyfin.ConfigDir, "encoding.xml"), "EncodingOptions", "EnableFallbackFont", "true")
	assertScalar(t, filepath.Join(cfg.Jellyfin.ConfigDir, "network.xml"), "NetworkConfiguration", "InternalHttpPort", "8097")
	assertScalar(t, filepath.Join(cfg.Jellyfin.ConfigDir, "network.xml"), "NetworkConfiguration", "EnableIPv6", "true")

	second, err := reconciler.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ChangedFiles) != 0 || len(second.BackupFiles) != 0 {
		t.Fatalf("idempotent result = %#v", second)
	}

	system, err := os.ReadFile(systemPath)
	if err != nil {
		t.Fatal(err)
	}
	changed, _, err := patchXML(system, "ServerConfiguration", map[string]elementChange{"ServerName": {scalar: "Dashboard changed"}, "KeepMe": {scalar: "new dashboard value"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(systemPath, changed, 0o640); err != nil {
		t.Fatal(err)
	}
	third, err := reconciler.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(third.ChangedFiles) != 1 {
		t.Fatalf("dashboard reconciliation result = %#v", third)
	}
	assertScalar(t, systemPath, "ServerConfiguration", "ServerName", "Managed")
	assertScalar(t, systemPath, "ServerConfiguration", "KeepMe", "new dashboard value")
}

func TestReconcileSkipsIncompleteSetup(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "system.xml"), `<ServerConfiguration><IsStartupWizardCompleted>false</IsStartupWizardCompleted></ServerConfiguration>`)
	cfg := &config.Config{Jellyfin: config.JellyfinConfig{ConfigDir: dir, Branding: config.BrandingConfig{LoginDisclaimer: optional("do not write")}}}
	result, err := New(cfg).Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if !result.SkippedSetup || len(result.ChangedFiles) != 0 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "branding.xml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("branding.xml was created during setup: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "system.xml")); err != nil {
		t.Fatal(err)
	}
	missing, err := New(cfg).Reconcile()
	if err != nil || !missing.SkippedSetup || len(missing.ChangedFiles) != 0 {
		t.Fatalf("missing setup result = %#v, error = %v", missing, err)
	}
}

func TestReconcileValidatesCustomAssetsBeforeWriting(t *testing.T) {
	cfg := fixtureConfig(t)
	cfg.Jellyfin.Branding.CustomCSSCode = optional(filepath.Join(cfg.Jellyfin.ConfigDir, "missing.css"))
	original, err := os.ReadFile(filepath.Join(cfg.Jellyfin.ConfigDir, "system.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg).Reconcile(); err == nil || !strings.Contains(err.Error(), "custom-css-code") {
		t.Fatalf("error = %v", err)
	}
	after, err := os.ReadFile(filepath.Join(cfg.Jellyfin.ConfigDir, "system.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("configuration changed after asset validation failure")
	}
}

func TestReconcileRollsBackEarlierFileWhenLaterWriteFails(t *testing.T) {
	cfg := fixtureConfig(t)
	paths := []string{filepath.Join(cfg.Jellyfin.ConfigDir, "encoding.xml"), filepath.Join(cfg.Jellyfin.ConfigDir, "system.xml")}
	original := make(map[string][]byte)
	for _, path := range paths {
		original[path], _ = os.ReadFile(path)
	}
	oldReplace := replaceConfigFile
	writes := 0
	replaceConfigFile = func(path string, data []byte, mode os.FileMode) error {
		writes++
		if writes == 2 {
			return errors.New("injected write failure")
		}
		return atomicWrite(path, data, mode)
	}
	t.Cleanup(func() { replaceConfigFile = oldReplace })
	if _, err := New(cfg).Reconcile(); err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Jellyfin.ConfigDir, "branding.xml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new branding.xml was not rolled back: %v", err)
	}
	for _, path := range paths {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(original[path]) {
			t.Fatalf("%s was not rolled back", filepath.Base(path))
		}
	}
}

func assertScalar(t *testing.T, path, root, name, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scalarElement(data, root, name)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s/%s = %q, want %q", filepath.Base(path), name, got, want)
	}
}
