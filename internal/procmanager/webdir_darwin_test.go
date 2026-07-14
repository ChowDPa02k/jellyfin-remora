//go:build darwin

package procmanager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMacOSBundleWebDir(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "Jellyfin.app", "Contents", "MacOS", "Jellyfin")
	web := filepath.Join(root, "Jellyfin.app", "Contents", "Resources", "jellyfin-web")
	if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(web, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveWebDir(exe, "default")
	if err != nil || got != web {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
