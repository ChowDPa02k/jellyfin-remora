package jellyfin

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

// TestInstalledJellyfinWebSelectionLabels is opt-in because it starts three
// clean Jellyfin servers. It verifies labels copied from the installed web UI,
// not a Remora-owned localization table.
func TestInstalledJellyfinWebSelectionLabels(t *testing.T) {
	binary := os.Getenv("JELLYFIN_INTEGRATION_BIN")
	webDir := os.Getenv("JELLYFIN_INTEGRATION_WEB")
	if binary == "" || webDir == "" {
		t.Skip("set JELLYFIN_INTEGRATION_BIN and JELLYFIN_INTEGRATION_WEB to run")
	}
	for _, test := range []struct {
		name, display, language, region, displayCode, languageCode, regionCode string
	}{
		{name: "Arabic", display: "العربية", language: "Arabic", region: "Saudi Arabia", displayCode: "ar", languageCode: "ar", regionCode: "SA"},
		{name: "Korean", display: "한국어", language: "Korean", region: "Korea", displayCode: "ko", languageCode: "ko", regionCode: "KR"},
		{name: "German", display: "Deutsch", language: "German", region: "Germany", displayCode: "de", languageCode: "de", regionCode: "DE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"data", "config", "cache", "logs"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(binary,
				"--datadir", filepath.Join(root, "data"),
				"--configdir", filepath.Join(root, "config"),
				"--cachedir", filepath.Join(root, "cache"),
				"--logdir", filepath.Join(root, "logs"),
				"--webdir", webDir,
			)
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			defer stopIntegrationProcess(t, cmd, exited)

			client := New("http://127.0.0.1:8096", 5*time.Second)
			deadline := time.Now().Add(30 * time.Second)
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				var startupUser StartupUser
				err := client.do(ctx, "GET", "/Startup/User", "", nil, &startupUser, 200)
				cancel()
				if err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("Jellyfin did not become ready: %v", err)
				}
				time.Sleep(250 * time.Millisecond)
			}

			cfg := config.InitConfig{
				ServerName:                "Remora " + test.name,
				DisplayLanguage:           test.display,
				User:                      "admin",
				Password:                  "integration-secret",
				PreferredMetadataLanguage: test.language,
				PreferredMetadataRegion:   test.region,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, err := client.CompleteStartup(ctx, cfg)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			assertSystemXMLSelections(t, filepath.Join(root, "config", "system.xml"), test.displayCode, test.languageCode, test.regionCode)
		})
	}
}

func assertSystemXMLSelections(t *testing.T, path, display, language, region string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			text := string(data)
			missing := ""
			for element, value := range map[string]string{"UICulture": display, "PreferredMetadataLanguage": language, "MetadataCountryCode": region} {
				if !strings.Contains(text, fmt.Sprintf("<%s>%s</%s>", element, value, element)) {
					missing = element
					break
				}
			}
			if missing == "" {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("system.xml omitted %s selection:\n%s", missing, text)
			}
		} else if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func stopIntegrationProcess(t *testing.T, cmd *exec.Cmd, exited <-chan error) {
	t.Helper()
	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
	}
}
