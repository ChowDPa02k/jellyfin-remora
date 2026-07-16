//go:build linux

package procmanager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxDefaultWebDirFindsPortableSibling(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	web := filepath.Join(root, "jellyfin-web")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(web, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := platformDefaultWebDir(filepath.Join(bin, "jellyfin"))
	if err != nil {
		t.Fatal(err)
	}
	if got != web {
		t.Fatalf("web dir = %q, want %q", got, web)
	}
}
