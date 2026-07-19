package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
	"github.com/ChowDPa02K/jellyfin-remora/internal/platform"
	"github.com/ChowDPa02K/jellyfin-remora/internal/storage"
	"github.com/ChowDPa02K/jellyfin-remora/sample"
)

type serviceArtifact struct {
	Kind string
	Path string
}

type initStorageChecker interface {
	InspectDisk(context.Context, int) model.StorageResult
	CheckDiskForInit(context.Context, int, bool) model.StorageResult
	CheckPaths(context.Context) []model.StorageResult
}

var (
	editConfigFile           = openConfigEditor
	locateRemoraExecutable   = siblingRemoraExecutable
	confirmInitAction        = promptInitConfirmation
	createInitStorageChecker = func(cfg *config.Config, executable string) (initStorageChecker, error) {
		return storage.NewForInit(cfg, platform.New(), executable)
	}
	initServicePrivileged = platformServicePrivileged
	installInitService    = installPlatformService
	startInitService      = startPlatformService
)

func runInit(args []string) error {
	fs := flag.NewFlagSet("remoractl init", flag.ContinueOnError)
	sampleDir := fs.String("sample-dir", "", "directory containing platform configuration samples")
	editor := fs.String("editor", "", "editor executable; defaults to $VISUAL, $EDITOR, vi, then nano")
	volume := fs.String("volume", "", "Windows physical-volume mount point, such as D:\\")
	dataRoot := fs.String("data-root", "", "Windows data root beneath the selected volume; defaults to <volume>\\jellyfin")
	noEdit := fs.Bool("no-edit", false, "use a fully prepared sample without opening an editor")
	force := fs.Bool("force", false, "allow --no-edit to replace an existing remora-config.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: remoractl init [--sample-dir DIR] [--editor EDITOR | --no-edit [--force]] [--volume D:\\] [--data-root PATH]")
	}
	if *noEdit && *editor != "" {
		return errors.New("--editor and --no-edit are mutually exclusive")
	}
	if *force && !*noEdit {
		return errors.New("--force is only valid with --no-edit")
	}
	remoraExecutable, err := locateRemoraExecutable()
	if err != nil {
		return err
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	destination := filepath.Join(workingDir, "remora-config.yaml")
	existing, destinationExists, err := existingInitConfiguration(destination)
	if err != nil {
		return err
	}
	if destinationExists && *noEdit && !*force {
		return errors.New("remora-config.yaml already exists; use interactive init to edit it, or pass --no-edit --force to replace it")
	}

	template := existing
	if !destinationExists || *noEdit {
		template, err = loadPlatformSample(*sampleDir)
		if err != nil {
			return err
		}
		template, err = preparePlatformTemplate(template, *volume, *dataRoot)
		if err != nil {
			return err
		}
	}
	if *noEdit && hasUnresolvedInitPlaceholder(template) {
		return errors.New("--no-edit requires a fully prepared sample without REPLACE-WITH placeholders")
	}
	temporary, cleanupTemporary, err := createSensitiveTemp("jellyfin-remora-config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer cleanupTemporary()
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(template); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}

	if !*noEdit {
		selectedEditor, err := chooseEditor(*editor)
		if err != nil {
			return err
		}
		if err := editConfigFile(selectedEditor, temporaryPath); err != nil {
			return err
		}
	}
	cfg, err := config.Load(temporaryPath)
	if err != nil {
		return fmt.Errorf("edited configuration is invalid; no files were changed: %w", err)
	}
	acceptedMismatches, err := validateInitStorage(cfg, remoraExecutable)
	if err != nil {
		return err
	}
	if err := preparePlatformInitDirectories(cfg, acceptedMismatches); err != nil {
		return fmt.Errorf("prepare platform directories: %w", err)
	}
	if err := validateInitPaths(cfg, remoraExecutable); err != nil {
		return err
	}
	edited, err := os.ReadFile(temporaryPath)
	if err != nil {
		return err
	}
	if destinationExists {
		backup, err := backupInitConfiguration(destination, existing)
		if err != nil {
			return fmt.Errorf("back up existing configuration: %w", err)
		}
		fmt.Printf("configuration backup written: %s\n", backup)
	}
	if err := atomicWriteFile(destination, edited, 0o600); err != nil {
		return fmt.Errorf("write configuration: %w", err)
	}

	artifact, err := generatePlatformService(cfg, remoraExecutable, destination)
	if err != nil {
		return err
	}
	fmt.Printf("configuration written: %s\n", destination)
	if artifact != nil {
		fmt.Printf("%s generated: %s\n", artifact.Kind, artifact.Path)
		if !initServicePrivileged() {
			fmt.Fprintf(os.Stderr, "WARNING: insufficient privileges to install %s; the generated file was kept in %s\n", artifact.Kind, workingDir)
			fmt.Fprintln(os.Stderr, platformServiceInstallInstructions(artifact))
			return nil
		}
		if err := installInitService(artifact); err != nil {
			return fmt.Errorf("install %s: %w", artifact.Kind, err)
		}
		fmt.Printf("%s installed\n", artifact.Kind)
		start, err := confirmInitAction("Start Jellyfin Remora now?")
		if err != nil {
			return err
		}
		if start {
			if err := startInitService(artifact); err != nil {
				return fmt.Errorf("start Jellyfin Remora: %w", err)
			}
			fmt.Println("Jellyfin Remora started")
		}
	}
	return nil
}

var initBackupNow = time.Now

func existingInitConfiguration(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect existing configuration: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, errors.New("existing remora-config.yaml must be a regular non-symlink file")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read existing configuration: %w", err)
	}
	return contents, true, nil
}

func backupInitConfiguration(path string, contents []byte) (string, error) {
	timestamp := initBackupNow().UTC().Format("20060102T150405.000000000Z")
	backup := path + ".bak-" + timestamp
	if err := atomicWriteFile(backup, contents, 0o600); err != nil {
		return "", err
	}
	return backup, nil
}

func validateInitStorage(cfg *config.Config, remoraExecutable string) (map[int]bool, error) {
	acceptedMismatches := make(map[int]bool)
	if len(cfg.Disks) == 0 {
		return acceptedMismatches, nil
	}
	checker, err := createInitStorageChecker(cfg, remoraExecutable)
	if err != nil {
		return nil, fmt.Errorf("create storage checker: %w", err)
	}
	for index, disk := range cfg.Disks {
		timeout := cfg.Remora.IOTimeout.Duration * 3
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		inspected := checker.InspectDisk(ctx, index)
		wasMounted := inspected.Mounted
		allowMismatch := false
		if mountSourceMismatch(inspected) {
			allowMismatch, err = confirmMountSourceMismatch(index, disk, inspected)
			if err != nil {
				cancel()
				return nil, err
			}
			if !allowMismatch {
				cancel()
				return nil, fmt.Errorf("storage[%d] mount source mismatch was not accepted", index)
			}
			acceptedMismatches[index] = true
		}

		result := inspected
		if !inspected.Healthy || allowMismatch {
			if inspected.Mounted && !mountSourceMismatch(inspected) {
				if _, err := preparePlatformInitProbePath(cfg, index, allowMismatch); err != nil {
					cancel()
					return nil, fmt.Errorf("prepare storage[%d] probe path: %w", index, err)
				}
			}
			result = checker.CheckDiskForInit(ctx, index, allowMismatch)
			if result.Fatal && result.Mounted && !mountSourceMismatch(result) {
				prepared, err := preparePlatformInitProbePath(cfg, index, allowMismatch)
				if err != nil {
					cancel()
					return nil, fmt.Errorf("prepare storage[%d] probe path: %w", index, err)
				}
				if prepared {
					result = checker.CheckDiskForInit(ctx, index, allowMismatch)
				}
			}
		}
		if mountSourceMismatch(result) && !allowMismatch {
			allowMismatch, err = confirmMountSourceMismatch(index, disk, result)
			if err != nil {
				cancel()
				return nil, err
			}
			if !allowMismatch {
				cancel()
				return nil, fmt.Errorf("storage[%d] mount source mismatch was not accepted", index)
			}
			acceptedMismatches[index] = true
			if result.Mounted {
				if _, err := preparePlatformInitProbePath(cfg, index, true); err != nil {
					cancel()
					return nil, fmt.Errorf("prepare storage[%d] probe path: %w", index, err)
				}
			}
			result = checker.CheckDiskForInit(ctx, index, true)
		}
		cancel()
		if result.Fatal {
			return nil, fmt.Errorf("storage[%d] %s validation failed: %s", index, disk.Target, result.Message)
		}
		if !result.Healthy {
			fmt.Fprintf(os.Stderr, "WARNING: storage[%d] %s: %s\n", index, disk.Target, result.Message)
		}
		action := "existing mount verified"
		if !wasMounted {
			action = "mounted and verified"
		}
		access := "readable"
		if disk.Permission == "rw" {
			access = "readable and writable"
		}
		fmt.Printf("storage[%d] %s: %s; %s\n", index, disk.Target, action, access)
	}
	return acceptedMismatches, nil
}

func validateInitPaths(cfg *config.Config, remoraExecutable string) error {
	checker, err := createInitStorageChecker(cfg, remoraExecutable)
	if err != nil {
		return fmt.Errorf("create path checker: %w", err)
	}
	timeout := cfg.Remora.IOTimeout.Duration * 4
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for _, result := range checker.CheckPaths(ctx) {
		if result.Fatal || !result.Healthy {
			return fmt.Errorf("Jellyfin path %s validation failed: %s", result.Target, result.Message)
		}
		fmt.Printf("Jellyfin path %s: prepared and writable\n", result.Target)
	}
	return nil
}

func mountSourceMismatch(result model.StorageResult) bool {
	return strings.HasPrefix(result.Message, "mount source mismatch:")
}

func confirmMountSourceMismatch(index int, disk config.DiskConfig, result model.StorageResult) (bool, error) {
	configuredIdentity := disk.Device
	if configuredIdentity == "" {
		configuredIdentity = disk.UUID
	}
	if configuredIdentity == "" {
		configuredIdentity = disk.VolumeGUID
	}
	fmt.Fprintf(os.Stderr, "WARNING: storage[%d] target %s does not match configured device %s (%s)\n", index, disk.Target, configuredIdentity, result.Message)
	fmt.Fprintln(os.Stderr, "Runtime supervision will still fence this mismatch until the configuration or mount is corrected.")
	return confirmInitAction("Continue initialization despite this mount mismatch?")
}

func promptInitConfirmation(question string) (bool, error) {
	fmt.Printf("%s [y/N]: ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func hasUnresolvedInitPlaceholder(template []byte) bool {
	return strings.Contains(strings.ToUpper(string(template)), "REPLACE-WITH")
}

func loadPlatformSample(configuredDir string) ([]byte, error) {
	name, err := platformSampleName()
	if err != nil {
		return nil, err
	}
	if configuredDir != "" {
		path := filepath.Join(configuredDir, name)
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read platform sample override %s: %w", path, err)
		}
		return contents, nil
	}
	contents, err := sample.Files.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read embedded platform sample %s: %w", name, err)
	}
	return contents, nil
}

func chooseEditor(configured string) (string, error) {
	for _, candidate := range []string{configured, os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi", "nano"} {
		if candidate == "" {
			continue
		}
		if strings.ContainsAny(candidate, " \t\r\n") {
			return "", fmt.Errorf("editor must be a single executable path: %q", candidate)
		}
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, nil
		}
		if configured != "" {
			return "", fmt.Errorf("editor %q not found", configured)
		}
	}
	return "", errors.New("neither vi nor nano is available; set $VISUAL or $EDITOR")
}

func openConfigEditor(editor, path string) error {
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor failed: %w", err)
	}
	return nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlink: %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".jellyfin-remora-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func siblingRemoraExecutable() (string, error) {
	ctl, err := os.Executable()
	if err != nil {
		return "", err
	}
	name := remoraExecutableName()
	path := filepath.Join(filepath.Dir(ctl), name)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("jellyfin-remora not found beside remoractl at %s: %w", path, err)
	}
	if !initExecutableUsable(info) {
		return "", fmt.Errorf("jellyfin-remora beside remoractl is not an executable file: %s", path)
	}
	return path, nil
}
