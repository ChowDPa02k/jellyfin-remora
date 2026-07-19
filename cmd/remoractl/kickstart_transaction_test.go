package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/kickstart"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

func TestDeployKickstartUnwindsCompletedStepsAfterStartFailure(t *testing.T) {
	root := t.TempDir()
	useInitWorkingDirectory(t, root)
	home := filepath.Join(root, "jellyfin-home")
	executable := filepath.Join(root, "jellyfin")
	if err := os.WriteFile(executable, []byte("placeholder"), 0o750); err != nil {
		t.Fatal(err)
	}
	oldCreate := createInitStorageChecker
	oldPrivileged := initServicePrivileged
	oldInstall := installInitService
	oldStart := startInitService
	createInitStorageChecker = func(cfg *config.Config, _ string) (initStorageChecker, error) {
		results := make([]model.StorageResult, 0, 4)
		for _, path := range []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir} {
			results = append(results, model.StorageResult{Target: path, Healthy: true, Writable: true})
		}
		return &fakeInitChecker{
			inspect: model.StorageResult{Mounted: true, Healthy: true, Writable: true},
			paths:   results,
		}, nil
	}
	initServicePrivileged = func() bool { return true }
	installInitService = func(*serviceArtifact) error { return nil }
	startInitService = func(*serviceArtifact) error { return errors.New("injected start failure") }
	t.Cleanup(func() {
		createInitStorageChecker = oldCreate
		initServicePrivileged = oldPrivileged
		installInitService = oldInstall
		startInitService = oldStart
	})
	answers := kickstart.Answers{
		Installation: kickstart.Installation{Executable: executable}, Home: home,
		ServerName: "Rollback Test", DisplayLanguage: "English",
		MetadataLanguage: "English", MetadataRegion: "United States",
		AdminPassword: "test-password",
	}
	err := deployKickstart(answers, executable, true)
	if err == nil || !strings.Contains(err.Error(), "injected start failure") {
		t.Fatalf("deployKickstart error = %v", err)
	}
	for _, path := range []string{
		home,
		filepath.Join(root, "remora-config.yaml"),
		kickstartServiceArtifactPath(filepath.Join(root, "remora-config.yaml")),
	} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("rollback left %s: %v", path, statErr)
		}
	}
}

func TestKickstartTransactionRollsBackInReverseOrder(t *testing.T) {
	transaction := &kickstartTransaction{}
	var order []string
	for _, name := range []string{"extract server", "write config", "install service"} {
		name := name
		transaction.record(name, func() error {
			order = append(order, name)
			return nil
		})
	}
	cause := errors.New("injected start failure")
	if err := transaction.fail(cause); !errors.Is(err, cause) {
		t.Fatalf("rollback error = %v", err)
	}
	want := []string{"install service", "write config", "extract server"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("rollback order = %v, want %v", order, want)
	}
}

func TestKickstartTransactionRestoresFilesAndRemovesCreatedPaths(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "remora-config.yaml")
	created := filepath.Join(root, "server")
	if err := os.WriteFile(existing, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	transaction := &kickstartTransaction{}
	if err := transaction.capturePath(existing); err != nil {
		t.Fatal(err)
	}
	if err := transaction.capturePath(created); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(created, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := transaction.fail(errors.New("injected service failure")); err == nil {
		t.Fatal("expected deployment failure")
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "original" {
		t.Fatalf("restored config = %q, error = %v", data, err)
	}
	if _, err := os.Stat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created server remained after rollback: %v", err)
	}
}

func TestKickstartTransactionReportsExactCleanupManifest(t *testing.T) {
	transaction := &kickstartTransaction{}
	transaction.record("/tmp/remora-config.yaml", func() error { return errors.New("permission denied") })
	transaction.record("installed systemd service", func() error { return errors.New("systemctl unavailable") })
	err := transaction.fail(errors.New("injected install failure"))
	want := "kickstart rollback incomplete; cleanup required:\n" +
		"  - installed systemd service: systemctl unavailable\n" +
		"  - /tmp/remora-config.yaml: permission denied"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("rollback error = %v", err)
	}
}
