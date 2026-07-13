package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathReadWrite(t *testing.T) {
	d := t.TempDir()
	if err := Path(d, "rw"); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(d, ".remora-probe-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("probe files left behind: %v %v", matches, err)
	}
}
func TestPathMissing(t *testing.T) {
	if err := Path(filepath.Join(t.TempDir(), "missing"), "r"); err == nil {
		t.Fatal("expected error")
	}
}
func TestPathReadOnlyModeDoesNotWrite(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "entry"), []byte("x"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := Path(d, "r"); err != nil {
		t.Fatal(err)
	}
}
