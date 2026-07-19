package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

func TestRunInitValidatesAndWritesConfiguration(t *testing.T) {
	root := t.TempDir()
	useInitWorkingDirectory(t, root)
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
	editConfigFile = func(_, _ string) error { return nil }
	stubInitLifecycle(t, filepath.Join(root, "jellyfin-remora"))
	t.Cleanup(func() { editConfigFile = oldEdit })

	editor, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := runInit([]string{"--sample-dir", sampleDir, "--editor", editor}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "remora-config.yaml")
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
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("configuration mode = %o, want 600", info.Mode().Perm())
	}
	if runtime.GOOS == "darwin" {
		plistPath := filepath.Join(root, "io.github.chowdpa02k.jellyfin-remora.plist")
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
	useInitWorkingDirectory(t, root)
	stubInitLifecycle(t, filepath.Join(root, "jellyfin-remora"))
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
	destination := filepath.Join(root, "remora-config.yaml")
	const original = "existing configuration\n"
	if err := os.WriteFile(destination, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	oldEdit := editConfigFile
	editConfigFile = func(_, path string) error {
		return os.WriteFile(path, []byte("not: [valid"), 0o600)
	}
	t.Cleanup(func() { editConfigFile = oldEdit })

	editor, executableErr := os.Executable()
	if executableErr != nil {
		t.Fatal(executableErr)
	}
	err = runInit([]string{"--sample-dir", sampleDir, "--editor", editor})
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

func TestRunInitNoEditUsesPreparedSample(t *testing.T) {
	root := t.TempDir()
	useInitWorkingDirectory(t, root)
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

	oldEdit := editConfigFile
	editConfigFile = func(_, _ string) error { return fmt.Errorf("editor must not run") }
	stubInitLifecycle(t, filepath.Join(root, "jellyfin-remora"))
	t.Cleanup(func() { editConfigFile = oldEdit })

	if err := runInit([]string{"--sample-dir", sampleDir, "--no-edit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "remora-config.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestRunInitRerunEditsExistingConfigurationAndCreatesBackup(t *testing.T) {
	root := t.TempDir()
	useInitWorkingDirectory(t, root)
	stubInitLifecycle(t, filepath.Join(root, "jellyfin-remora"))
	configDir := filepath.Join(root, "jellyfin-config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := minimalInitConfig(configDir) + "# existing operator configuration\n"
	destination := filepath.Join(root, "remora-config.yaml")
	if err := os.WriteFile(destination, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	oldEdit := editConfigFile
	oldNow := initBackupNow
	editConfigFile = func(_, path string) error {
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(contents) != existing {
			return fmt.Errorf("editor received sample instead of existing config: %q", contents)
		}
		return os.WriteFile(path, append(contents, []byte("# edited on rerun\n")...), 0o600)
	}
	initBackupNow = func() time.Time { return time.Date(2026, 7, 19, 12, 34, 56, 789, time.UTC) }
	t.Cleanup(func() {
		editConfigFile = oldEdit
		initBackupNow = oldNow
	})

	editor, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := runInit([]string{"--sample-dir", filepath.Join(root, "sample-does-not-exist"), "--editor", editor}); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != existing+"# edited on rerun\n" {
		t.Fatalf("rerun result = %q", written)
	}
	backup := destination + ".bak-20260719T123456.000000789Z"
	backedUp, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(backedUp) != existing {
		t.Fatalf("backup = %q, want original %q", backedUp, existing)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(backup)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestRunInitNoEditRequiresForceToReplaceExistingConfiguration(t *testing.T) {
	root := t.TempDir()
	useInitWorkingDirectory(t, root)
	stubInitLifecycle(t, filepath.Join(root, "jellyfin-remora"))
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
	replacement := minimalInitConfig(configDir) + "# replacement sample\n"
	if err := os.WriteFile(filepath.Join(sampleDir, sampleName), []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "remora-config.yaml")
	original := minimalInitConfig(configDir) + "# keep without force\n"
	if err := os.WriteFile(destination, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	err = runInit([]string{"--sample-dir", sampleDir, "--no-edit"})
	if err == nil || !strings.Contains(err.Error(), "--no-edit --force") {
		t.Fatalf("runInit error = %v, want explicit force requirement", err)
	}
	unchanged, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != original {
		t.Fatalf("configuration changed without --force: %q", unchanged)
	}
	backups, err := filepath.Glob(destination + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("refused overwrite created backups: %v", backups)
	}

	oldNow := initBackupNow
	initBackupNow = func() time.Time { return time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { initBackupNow = oldNow })
	if err := runInit([]string{"--sample-dir", sampleDir, "--no-edit", "--force"}); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != replacement {
		t.Fatalf("forced replacement = %q, want sample %q", written, replacement)
	}
	backedUp, err := os.ReadFile(destination + ".bak-20260719T130000.000000000Z")
	if err != nil {
		t.Fatal(err)
	}
	if string(backedUp) != original {
		t.Fatalf("forced replacement backup = %q, want %q", backedUp, original)
	}
}

func TestRunInitNoEditRejectsPlaceholders(t *testing.T) {
	root := t.TempDir()
	useInitWorkingDirectory(t, root)
	stubInitLifecycle(t, filepath.Join(root, "jellyfin-remora"))
	sampleDir := filepath.Join(root, "sample")
	if err := os.Mkdir(sampleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sampleName, err := platformSampleName()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sampleDir, sampleName), []byte("value: REPLACE-WITH-SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = runInit([]string{"--sample-dir", sampleDir, "--no-edit"})
	if err == nil || !strings.Contains(err.Error(), "fully prepared sample") {
		t.Fatalf("runInit error = %v", err)
	}
}

func TestRunInitChecksSiblingDaemonBeforeReadingSample(t *testing.T) {
	root := t.TempDir()
	useInitWorkingDirectory(t, root)
	oldLocate := locateRemoraExecutable
	locateRemoraExecutable = func() (string, error) { return "", errors.New("jellyfin-remora not found") }
	t.Cleanup(func() { locateRemoraExecutable = oldLocate })
	err := runInit([]string{"--sample-dir", filepath.Join(root, "missing"), "--no-edit"})
	if err == nil || !strings.Contains(err.Error(), "jellyfin-remora not found") {
		t.Fatalf("runInit error = %v", err)
	}
}

func TestLoadPlatformSampleUsesEmbeddedTemplateOutsideRepository(t *testing.T) {
	useInitWorkingDirectory(t, t.TempDir())
	template, err := loadPlatformSample("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(template), "config-version: 2") {
		t.Fatalf("embedded platform template is unexpected: %q", template)
	}
}

func TestRunInitInstallsArtifactAndStartsAfterConfirmation(t *testing.T) {
	root := t.TempDir()
	useInitWorkingDirectory(t, root)
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
	stubInitLifecycle(t, filepath.Join(root, "jellyfin-remora"))
	oldEdit := editConfigFile
	oldConfirm := confirmInitAction
	editConfigFile = func(_, _ string) error { return nil }
	confirmInitAction = func(string) (bool, error) { return true, nil }
	initServicePrivileged = func() bool { return true }
	installed := 0
	started := 0
	installInitService = func(*serviceArtifact) error { installed++; return nil }
	startInitService = func(*serviceArtifact) error { started++; return nil }
	t.Cleanup(func() {
		editConfigFile = oldEdit
		confirmInitAction = oldConfirm
	})
	editor, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := runInit([]string{"--sample-dir", sampleDir, "--editor", editor}); err != nil {
		t.Fatal(err)
	}
	if installed != 1 || started != 1 {
		t.Fatalf("installed=%d started=%d", installed, started)
	}
}

func TestValidateInitStorageConfirmsMismatchWithoutWeakeningRuntime(t *testing.T) {
	checker := &fakeInitChecker{
		inspect: model.StorageResult{Mounted: true, Fatal: true, Message: "mount source mismatch: got /dev/disk9s1"},
		check:   model.StorageResult{Mounted: true, Healthy: true, Writable: true, Message: "mount source mismatch: got /dev/disk9s1"},
	}
	oldCreate := createInitStorageChecker
	oldConfirm := confirmInitAction
	createInitStorageChecker = func(*config.Config, string) (initStorageChecker, error) { return checker, nil }
	confirmInitAction = func(string) (bool, error) { return true, nil }
	t.Cleanup(func() {
		createInitStorageChecker = oldCreate
		confirmInitAction = oldConfirm
	})
	cfg := initStorageConfig()
	accepted, err := validateInitStorage(cfg, "/path/to/jellyfin-remora")
	if err != nil {
		t.Fatal(err)
	}
	if !accepted[0] {
		t.Fatalf("accepted mismatches = %v", accepted)
	}
	if len(checker.allowMismatch) != 1 || !checker.allowMismatch[0] {
		t.Fatalf("allowMismatch calls = %v", checker.allowMismatch)
	}
}

func TestValidateInitStorageStopsWhenMismatchIsDeclined(t *testing.T) {
	checker := &fakeInitChecker{inspect: model.StorageResult{Mounted: true, Fatal: true, Message: "mount source mismatch: got /dev/disk9s1"}}
	oldCreate := createInitStorageChecker
	oldConfirm := confirmInitAction
	createInitStorageChecker = func(*config.Config, string) (initStorageChecker, error) { return checker, nil }
	confirmInitAction = func(string) (bool, error) { return false, nil }
	t.Cleanup(func() {
		createInitStorageChecker = oldCreate
		confirmInitAction = oldConfirm
	})
	_, err := validateInitStorage(initStorageConfig(), "/path/to/jellyfin-remora")
	if err == nil || !strings.Contains(err.Error(), "was not accepted") {
		t.Fatalf("validateInitStorage error = %v", err)
	}
	if len(checker.allowMismatch) != 0 {
		t.Fatalf("declined mismatch was probed: %v", checker.allowMismatch)
	}
}

func TestValidateInitStorageMountsOnlyMissingTarget(t *testing.T) {
	checker := &fakeInitChecker{
		inspect: model.StorageResult{Mounted: false, Fatal: true, Message: "target is not mounted"},
		check:   model.StorageResult{Mounted: true, Healthy: true, Writable: true},
	}
	oldCreate := createInitStorageChecker
	createInitStorageChecker = func(*config.Config, string) (initStorageChecker, error) { return checker, nil }
	t.Cleanup(func() { createInitStorageChecker = oldCreate })
	if _, err := validateInitStorage(initStorageConfig(), "/path/to/jellyfin-remora"); err != nil {
		t.Fatal(err)
	}
	if len(checker.allowMismatch) != 1 || checker.allowMismatch[0] {
		t.Fatalf("allowMismatch calls = %v", checker.allowMismatch)
	}
}

func TestValidateInitStorageLeavesExistingHealthyMountAlone(t *testing.T) {
	checker := &fakeInitChecker{inspect: model.StorageResult{Mounted: true, Healthy: true, Writable: true}}
	oldCreate := createInitStorageChecker
	createInitStorageChecker = func(*config.Config, string) (initStorageChecker, error) { return checker, nil }
	t.Cleanup(func() { createInitStorageChecker = oldCreate })
	if _, err := validateInitStorage(initStorageConfig(), "/path/to/jellyfin-remora"); err != nil {
		t.Fatal(err)
	}
	if len(checker.allowMismatch) != 0 {
		t.Fatalf("existing healthy mount was remounted: %v", checker.allowMismatch)
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

type fakeInitChecker struct {
	inspect       model.StorageResult
	check         model.StorageResult
	paths         []model.StorageResult
	allowMismatch []bool
}

func (f *fakeInitChecker) InspectDisk(context.Context, int) model.StorageResult {
	return f.inspect
}

func (f *fakeInitChecker) CheckDiskForInit(_ context.Context, _ int, allowMismatch bool) model.StorageResult {
	f.allowMismatch = append(f.allowMismatch, allowMismatch)
	return f.check
}

func (f *fakeInitChecker) CheckPaths(context.Context) []model.StorageResult { return f.paths }

func initStorageConfig() *config.Config {
	return &config.Config{
		Remora: config.RemoraConfig{IOTimeout: config.Duration{Duration: time.Second}},
		Disks: []config.DiskConfig{{
			Type:       "physical",
			Device:     "/dev/disk5s1",
			Target:     "/Volumes/AppData",
			Permission: "rw",
		}},
	}
}

func useInitWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

func stubInitLifecycle(t *testing.T, executable string) {
	t.Helper()
	oldLocate := locateRemoraExecutable
	oldCreate := createInitStorageChecker
	oldPrivileged := initServicePrivileged
	oldInstall := installInitService
	oldStart := startInitService
	locateRemoraExecutable = func() (string, error) { return executable, nil }
	createInitStorageChecker = func(cfg *config.Config, _ string) (initStorageChecker, error) {
		paths := []string{cfg.Jellyfin.DataDir, cfg.Jellyfin.ConfigDir, cfg.Jellyfin.CacheDir, cfg.Jellyfin.LogDir}
		results := make([]model.StorageResult, 0, len(paths))
		for _, path := range paths {
			results = append(results, model.StorageResult{Target: path, Healthy: true, Writable: true})
		}
		return &fakeInitChecker{paths: results}, nil
	}
	initServicePrivileged = func() bool { return false }
	installInitService = func(*serviceArtifact) error { return nil }
	startInitService = func(*serviceArtifact) error { return nil }
	t.Cleanup(func() {
		locateRemoraExecutable = oldLocate
		createInitStorageChecker = oldCreate
		initServicePrivileged = oldPrivileged
		installInitService = oldInstall
		startInitService = oldStart
	})
}
