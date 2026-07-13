package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

type serviceArtifact struct {
	Kind string
	Path string
}

var (
	editConfigFile         = openConfigEditor
	locateRemoraExecutable = siblingRemoraExecutable
)

func runInit(args []string) error {
	fs := flag.NewFlagSet("remoractl init", flag.ContinueOnError)
	sampleDir := fs.String("sample-dir", "", "directory containing platform configuration samples")
	editor := fs.String("editor", "", "editor executable; defaults to $VISUAL, $EDITOR, vi, then nano")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: remoractl init [--sample-dir DIR] [--editor vi|nano]")
	}

	sample, err := findPlatformSample(*sampleDir)
	if err != nil {
		return err
	}
	template, err := os.ReadFile(sample)
	if err != nil {
		return fmt.Errorf("read platform sample: %w", err)
	}
	temporary, err := os.CreateTemp("", "jellyfin-remora-config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
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

	selectedEditor, err := chooseEditor(*editor)
	if err != nil {
		return err
	}
	if err := editConfigFile(selectedEditor, temporaryPath); err != nil {
		return err
	}
	cfg, err := config.Load(temporaryPath)
	if err != nil {
		return fmt.Errorf("edited configuration is invalid; no files were changed: %w", err)
	}
	configDirInfo, err := os.Stat(cfg.Jellyfin.ConfigDir)
	if err != nil {
		return fmt.Errorf("jellyfin.config-dir must already exist before init: %w", err)
	}
	if !configDirInfo.IsDir() {
		return fmt.Errorf("jellyfin.config-dir is not a directory: %s", cfg.Jellyfin.ConfigDir)
	}
	edited, err := os.ReadFile(temporaryPath)
	if err != nil {
		return err
	}
	destination := filepath.Join(cfg.Jellyfin.ConfigDir, "config.yaml")
	if err := atomicWriteFile(destination, edited, 0o600); err != nil {
		return fmt.Errorf("write configuration: %w", err)
	}

	remoraExecutable, err := locateRemoraExecutable()
	if err != nil {
		return err
	}
	artifact, err := generatePlatformService(cfg, remoraExecutable, destination)
	if err != nil {
		return err
	}
	fmt.Printf("configuration written: %s\n", destination)
	if artifact != nil {
		fmt.Printf("%s generated: %s\n", artifact.Kind, artifact.Path)
	}
	return nil
}

func platformSampleName() (string, error) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		return "config-" + runtime.GOOS + ".yaml", nil
	default:
		return "", fmt.Errorf("remoractl init does not support %s", runtime.GOOS)
	}
}

func findPlatformSample(configuredDir string) (string, error) {
	name, err := platformSampleName()
	if err != nil {
		return "", err
	}
	if configuredDir != "" {
		path := filepath.Join(configuredDir, name)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("platform sample %s: %w", path, err)
		}
		return path, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	executableDir := filepath.Dir(executable)
	workingDir, _ := os.Getwd()
	candidates := []string{
		filepath.Join(executableDir, "..", "share", "jellyfin-remora", "sample", name),
		filepath.Join(executableDir, "..", "sample", name),
		filepath.Join(workingDir, "sample", name),
	}
	for _, candidate := range candidates {
		if st, statErr := os.Stat(candidate); statErr == nil && !st.IsDir() {
			return filepath.Clean(candidate), nil
		}
	}
	return "", fmt.Errorf("cannot find %s; searched %s", name, strings.Join(candidates, ", "))
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
	name := "jellyfin-remora"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(filepath.Dir(ctl), name)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("jellyfin-remora executable must be installed beside remoractl: %w", err)
	}
	return path, nil
}
