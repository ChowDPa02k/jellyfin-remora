package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunInitValidatesAndWritesConfiguration(t *testing.T) {
	root := t.TempDir()
	sampleDir := filepath.Join(root, "sample")
	configDir := filepath.Join(root, "jellyfin-config")
	if err := os.MkdirAll(sampleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sampleName, err := platformSampleName()
	if err != nil {
		t.Fatal(err)
	}
	sample := minimalInitConfig(configDir)
	if err := os.WriteFile(filepath.Join(sampleDir, sampleName), []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}

	oldEdit := editConfigFile
	oldLocate := locateRemoraExecutable
	editConfigFile = func(_, _ string) error { return nil }
	locateRemoraExecutable = func() (string, error) { return filepath.Join(root, "jellyfin-remora"), nil }
	t.Cleanup(func() {
		editConfigFile = oldEdit
		locateRemoraExecutable = oldLocate
	})

	if err := runInit([]string{"--sample-dir", sampleDir, "--editor", "vi"}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(configDir, "config.yaml")
	written, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != sample {
		t.Fatalf("written configuration differs from edited sample\nwant:\n%s\ngot:\n%s", sample, written)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("configuration mode = %o, want 600", info.Mode().Perm())
	}
	if runtime.GOOS == "darwin" {
		plistPath := filepath.Join(configDir, "io.github.chowdpa02k.jellyfin-remora.plist")
		plist, readErr := os.ReadFile(plistPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, want := range []string{filepath.Join(root, "jellyfin-remora"), destination, "KeepAlive"} {
			if !strings.Contains(string(plist), want) {
				t.Fatalf("generated plist omitted %q:\n%s", want, plist)
			}
		}
	}
}

func TestRunInitRejectsInvalidEditWithoutReplacingConfiguration(t *testing.T) {
	root := t.TempDir()
	sampleDir := filepath.Join(root, "sample")
	configDir := filepath.Join(root, "jellyfin-config")
	if err := os.MkdirAll(sampleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sampleName, err := platformSampleName()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sampleDir, sampleName), []byte(minimalInitConfig(configDir)), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(configDir, "config.yaml")
	const original = "existing configuration\n"
	if err := os.WriteFile(destination, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	oldEdit := editConfigFile
	editConfigFile = func(_, path string) error {
		return os.WriteFile(path, []byte("not: [valid"), 0o600)
	}
	t.Cleanup(func() { editConfigFile = oldEdit })

	err = runInit([]string{"--sample-dir", sampleDir, "--editor", "vi"})
	if err == nil || !strings.Contains(err.Error(), "no files were changed") {
		t.Fatalf("runInit error = %v, want validation failure", err)
	}
	written, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(written) != original {
		t.Fatalf("invalid edit replaced configuration: %q", written)
	}
}

func TestAtomicWriteFileRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(link, []byte("replacement"), 0o600); err == nil {
		t.Fatal("atomicWriteFile accepted a symlink destination")
	}
}

func minimalInitConfig(configDir string) string {
	return fmt.Sprintf(`config-version: 2
remora:
  monitoring:
    interval: 1s
    jellyfin-api:
      interval: 10s
      failure-threshold: 3
jellyfin:
  path: /Applications/Jellyfin.app/Contents/MacOS/Jellyfin
  run-as-user: nobody
  data-dir: %s
  config-dir: %s
  cache-dir: %s
  log-dir: %s
`, filepath.Join(configDir, "data"), configDir, filepath.Join(configDir, "cache"), filepath.Join(configDir, "logs"))
}
