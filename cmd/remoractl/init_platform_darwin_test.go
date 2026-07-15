//go:build darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDarwinServiceInstallIsIdempotent(t *testing.T) {
	root := t.TempDir()
	artifactPath := filepath.Join(root, "generated.plist")
	if err := os.WriteFile(artifactPath, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldDirectory := darwinLaunchDaemonDirectory
	oldChown := darwinChown
	darwinLaunchDaemonDirectory = root
	darwinChown = func(string, int, int) error { return nil }
	t.Cleanup(func() {
		darwinLaunchDaemonDirectory = oldDirectory
		darwinChown = oldChown
	})
	artifact := &serviceArtifact{Kind: "launchd plist", Path: artifactPath}
	if err := installPlatformService(artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installPlatformService(artifact); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(root, darwinServiceLabel+".plist"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "second" {
		t.Fatalf("installed plist = %q", written)
	}
}

func TestDarwinServiceStartBootstrapsOrRestarts(t *testing.T) {
	oldRun := runDarwinLaunchctl
	t.Cleanup(func() { runDarwinLaunchctl = oldRun })

	var calls [][]string
	runDarwinLaunchctl = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "print" {
			return errors.New("not loaded")
		}
		return nil
	}
	if err := startPlatformService(nil); err != nil {
		t.Fatal(err)
	}
	wantBootstrap := [][]string{{"print", "system/" + darwinServiceLabel}, {"bootstrap", "system", darwinInstalledServicePath()}}
	if !reflect.DeepEqual(calls, wantBootstrap) {
		t.Fatalf("bootstrap calls = %v", calls)
	}

	calls = nil
	runDarwinLaunchctl = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	if err := startPlatformService(nil); err != nil {
		t.Fatal(err)
	}
	wantRestart := [][]string{{"print", "system/" + darwinServiceLabel}, {"bootout", "system/" + darwinServiceLabel}, {"bootstrap", "system", darwinInstalledServicePath()}}
	if !reflect.DeepEqual(calls, wantRestart) {
		t.Fatalf("restart calls = %v", calls)
	}
}
