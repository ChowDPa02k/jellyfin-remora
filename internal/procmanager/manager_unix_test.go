//go:build !windows

package procmanager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTarballLayoutPreservesLowercaseExecutable(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "jellyfin")
	web := filepath.Join(root, "jellyfin-web")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(web, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveExecutable(root)
	want, canonicalErr := filepath.EvalSymlinks(exe)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	if err != nil || got != want {
		t.Fatalf("executable=%q want=%q err=%v", got, want, err)
	}
	if gotWeb, err := resolveWebDir(got, web); err != nil || gotWeb != web {
		t.Fatalf("web=%q want=%q err=%v", gotWeb, web, err)
	}
}
