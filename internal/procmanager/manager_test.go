package procmanager

import (
	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveExecutableAndBuildArgs(t *testing.T) {
	d := t.TempDir()
	exe := filepath.Join(d, "Jellyfin")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveExecutable(d)
	if err != nil || got != exe {
		t.Fatalf("got=%q err=%v", got, err)
	}
	cfg := &config.Config{Jellyfin: config.JellyfinConfig{DataDir: "/d", ConfigDir: "/c", CacheDir: "/k", LogDir: "/l", Parameters: map[string]any{"hostwebclient": true}}}
	want := []string{"--datadir=/d", "--configdir=/c", "--cachedir=/k", "--logdir=/l", "--hostwebclient=true"}
	if args := buildArgs(cfg, ""); !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%v", args)
	}
}

func TestResolveMacOSBundleWebDir(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "Jellyfin.app", "Contents", "MacOS", "Jellyfin")
	web := filepath.Join(root, "Jellyfin.app", "Contents", "Resources", "jellyfin-web")
	if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(web, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveWebDir(exe, "default")
	if err != nil || got != web {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
