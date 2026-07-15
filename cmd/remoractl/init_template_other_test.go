//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func TestPreparePlatformInitDirectoriesCreatesMissingJellyfinTree(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "jellyfin")
	cfg := &config.Config{
		Disks: []config.DiskConfig{{Target: root}},
		Jellyfin: config.JellyfinConfig{
			DataDir: filepath.Join(base, "data"), ConfigDir: filepath.Join(base, "config"),
			CacheDir: filepath.Join(base, "cache"), LogDir: filepath.Join(base, "log"),
		},
	}
	if err := preparePlatformInitDirectories(cfg, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("prepared path %s: info=%v err=%v", path, info, err)
		}
	}
}

func TestPreparePlatformInitDirectoriesRejectsUnconfiguredPath(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Disks: []config.DiskConfig{{Target: filepath.Join(root, "storage")}},
		Jellyfin: config.JellyfinConfig{
			DataDir: filepath.Join(root, "outside", "data"), ConfigDir: filepath.Join(root, "storage"),
			CacheDir: filepath.Join(root, "storage"), LogDir: filepath.Join(root, "storage"),
		},
	}
	if err := os.Mkdir(cfg.Disks[0].Target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := preparePlatformInitDirectories(cfg, nil); err == nil {
		t.Fatal("unconfigured path was created")
	}
}

func TestPreparePlatformInitDirectoriesRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	storage := filepath.Join(root, "storage")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(storage, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(storage, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Disks: []config.DiskConfig{{Target: storage}},
		Jellyfin: config.JellyfinConfig{
			DataDir: filepath.Join(link, "data"), ConfigDir: storage, CacheDir: storage, LogDir: storage,
		},
	}
	if err := preparePlatformInitDirectories(cfg, nil); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}

func TestConfiguredStorageForPathUsesFilesystemIdentity(t *testing.T) {
	root := t.TempDir()
	storage := filepath.Join(root, "storage")
	if err := os.Mkdir(storage, 0o750); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "STORAGE")
	aliasInfo, err := os.Stat(alias)
	if err != nil {
		t.Skip("test filesystem is case-sensitive")
	}
	storageInfo, err := os.Stat(storage)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(storageInfo, aliasInfo) {
		t.Skip("case variants refer to different directories")
	}
	if _, ok := configuredStorageForPath(filepath.Join(alias, "jellyfin", "data"), []config.DiskConfig{{Target: storage}}); !ok {
		t.Fatal("case-insensitive alias of configured storage was rejected")
	}
}
