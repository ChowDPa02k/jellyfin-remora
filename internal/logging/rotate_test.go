package logging

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotatesBySize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remora.log")
	w, err := New(path, 4, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("5")); err != nil {
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
