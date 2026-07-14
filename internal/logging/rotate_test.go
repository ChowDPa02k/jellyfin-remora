package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotatesOnRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remora.log")
	w, err := New(path, 1024*1024, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err = w.Rotate(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("second\n")); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%d", len(entries))
	}
}

func TestLumberjackRotationKeepsJellyfinLogPattern(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jellyfin-console.log")
	w, err := New(path, 1024*1024, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("\x1b[32mfirst\x1b[0m\n")); err != nil {
		t.Fatal(err)
	}
	if err = w.Rotate(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("second\n")); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%d", len(entries))
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "jellyfin-") || !strings.HasSuffix(entry.Name(), ".log") {
			t.Fatalf("unexpected Jellyfin log name %q", entry.Name())
		}
	}
	active, err := os.ReadFile(path)
	if err != nil || string(active) != "second\n" {
		t.Fatalf("active log=%q err=%v", active, err)
	}
	var backup []byte
	for _, entry := range entries {
		if entry.Name() != filepath.Base(path) {
			backup, err = os.ReadFile(filepath.Join(filepath.Dir(path), entry.Name()))
		}
	}
	if err != nil || string(backup) != "\x1b[32mfirst\x1b[0m\n" {
		t.Fatalf("backup log=%q err=%v", backup, err)
	}
}
