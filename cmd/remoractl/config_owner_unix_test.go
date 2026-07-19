//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReplaceConfigurationFilePreservesOriginalOwner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := original.Sys().(*syscall.Stat_t)

	oldSetter := setConfigurationFileOwner
	var gotUID, gotGID int
	setConfigurationFileOwner = func(file *os.File, uid, gid int) error {
		gotUID, gotGID = uid, gid
		return oldSetter(file, uid, gid)
	}
	t.Cleanup(func() { setConfigurationFileOwner = oldSetter })

	if err := replaceConfigurationFile(path, []byte("new"), 0o600, original); err != nil {
		t.Fatal(err)
	}
	if gotUID != int(want.Uid) || gotGID != int(want.Gid) {
		t.Fatalf("owner callback=%d:%d want=%d:%d", gotUID, gotGID, want.Uid, want.Gid)
	}
	current, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got := current.Sys().(*syscall.Stat_t)
	if got.Uid != want.Uid || got.Gid != want.Gid {
		t.Fatalf("owner after replace=%d:%d want=%d:%d", got.Uid, got.Gid, want.Uid, want.Gid)
	}
}

func TestReplaceConfigurationFileOwnerFailurePreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	oldSetter := setConfigurationFileOwner
	setConfigurationFileOwner = func(*os.File, int, int) error { return syscall.EPERM }
	t.Cleanup(func() { setConfigurationFileOwner = oldSetter })

	if err := replaceConfigurationFile(path, []byte("new"), 0o600, original); err == nil {
		t.Fatal("owner preservation failure was ignored")
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "old" {
		t.Fatalf("original=%q err=%v", contents, err)
	}
}
