package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTemporaryConfigurationSignalsRemoveSecretsBeforeTermination(t *testing.T) {
	patterns := []string{
		"jellyfin-remora-config-*.yaml",
		"jellyfin-remora-edit-*.yaml",
		"jellyfin-remora-kickstart-*.yaml",
	}
	for _, pattern := range patterns {
		for _, received := range temporaryFileSignals() {
			t.Run(filepath.Base(pattern)+"/"+received.String(), func(t *testing.T) {
				file, err := os.CreateTemp(t.TempDir(), pattern)
				if err != nil {
					t.Fatal(err)
				}
				path := file.Name()
				if _, err := file.WriteString("admin-password: secret\nwatchdog-password: secret\n"); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}

				guard := &temporaryFileCleanup{
					path:    path,
					signals: make(chan os.Signal, 1),
					stop:    make(chan struct{}),
				}
				terminated := make(chan error, 1)
				go guard.run(func(os.Signal) {
					_, statErr := os.Stat(path)
					if !errors.Is(statErr, os.ErrNotExist) {
						terminated <- statErr
						return
					}
					terminated <- nil
				})
				guard.signals <- received

				select {
				case err := <-terminated:
					if err != nil {
						t.Fatalf("temporary secret still existed at termination: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("signal cleanup did not complete")
				}
			})
		}
	}
}

func TestTemporaryConfigurationNormalReturnRemovesFile(t *testing.T) {
	file, cleanup, err := createSensitiveTemp("jellyfin-remora-config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file remained after cleanup: %v", err)
	}
}
