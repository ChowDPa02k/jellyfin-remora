package main

import (
	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareJellyfinPathsOnlyOnVerifiedStorage(t *testing.T) {
	old := effectiveUID
	effectiveUID = func() int { return 501 }
	defer func() { effectiveUID = old }()
	mount := t.TempDir()
	cfg := &config.Config{Disks: []config.DiskConfig{{Target: mount}}, Jellyfin: config.JellyfinConfig{DataDir: filepath.Join(mount, "jellyfin/data"), ConfigDir: filepath.Join(mount, "jellyfin/config"), CacheDir: filepath.Join(mount, "jellyfin/cache"), LogDir: filepath.Join(mount, "jellyfin/log")}}
	prepared, err := prepareJellyfinPaths(cfg, []model.StorageResult{{Healthy: true, Target: mount}})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 4 {
		t.Fatalf("prepared=%v", prepared)
	}
	for _, path := range prepared {
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			t.Fatalf("path=%s err=%v", path, err)
		}
	}
}
func TestPrepareRejectsUncoveredPath(t *testing.T) {
	old := effectiveUID
	effectiveUID = func() int { return 501 }
	defer func() { effectiveUID = old }()
	mount := t.TempDir()
	cfg := &config.Config{Disks: []config.DiskConfig{{Target: mount}}, Jellyfin: config.JellyfinConfig{DataDir: filepath.Join(t.TempDir(), "outside"), ConfigDir: mount, CacheDir: mount, LogDir: mount}}
	if _, err := prepareJellyfinPaths(cfg, []model.StorageResult{{Healthy: true, Target: mount}}); err == nil {
		t.Fatal("expected uncovered path rejection")
	}
}
