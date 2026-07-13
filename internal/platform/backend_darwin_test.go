//go:build darwin

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureMountTargetCreatesMissingDirectory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nested", "mount")
	if err := ensureMountTarget(target); err != nil {
		t.Fatalf("ensureMountTarget() error = %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target mode = %v, want directory", info.Mode())
	}
}

func TestEnsureMountTargetRejectsUnsafeTargets(t *testing.T) {
	for _, target := range []string{"", "relative", string(filepath.Separator)} {
		t.Run(strings.ReplaceAll(target, string(filepath.Separator), "root"), func(t *testing.T) {
			if err := ensureMountTarget(target); err == nil {
				t.Fatalf("ensureMountTarget(%q) succeeded, want error", target)
			}
		})
	}
}

func TestEnsureMountTargetRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	realTarget := filepath.Join(root, "real")
	if err := os.Mkdir(realTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realTarget, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureMountTarget(link); err == nil {
		t.Fatal("ensureMountTarget() succeeded for symlink, want error")
	}
}
