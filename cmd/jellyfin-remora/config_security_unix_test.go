//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateConfigFileSecurityRequiresOwnerOnlyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("general: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateConfigFileSecurity(path); err != nil {
		t.Fatalf("owner-only configuration rejected: %v", err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := validateConfigFileSecurity(path); err == nil {
		t.Fatal("group-readable configuration was accepted")
	}
}

func TestValidateConfigFileSecurityPropagatesStatFailure(t *testing.T) {
	if err := validateConfigFileSecurity(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing configuration was accepted")
	}
}
