//go:build darwin

package main

import (
	"path/filepath"
	"testing"
)

func TestInstanceLockRejectsDuplicateAndCanBeReacquired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remora.sock")
	one, err := acquireInstanceLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if two, err := acquireInstanceLock(path); err == nil {
		_ = two.Close()
		t.Fatal("duplicate instance acquired lock")
	}
	if err := one.Close(); err != nil {
		t.Fatal(err)
	}
	three, err := acquireInstanceLock(path)
	if err != nil {
		t.Fatalf("lock was not reusable: %v", err)
	}
	_ = three.Close()
}
