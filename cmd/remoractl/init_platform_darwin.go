//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"howett.net/plist"
)

const darwinServiceLabel = "io.github.chowdpa02k.jellyfin-remora"

var (
	darwinLaunchDaemonDirectory = "/Library/LaunchDaemons"
	runDarwinLaunchctl          = runLaunchctl
	darwinChown                 = os.Chown
)

func platformSampleName() (string, error) { return "config-darwin.yaml", nil }
func remoraExecutableName() string        { return "jellyfin-remora" }

func generatePlatformService(_ *config.Config, executable, configPath string) (*serviceArtifact, error) {
	payload := map[string]any{
		"Label":             darwinServiceLabel,
		"ProgramArguments":  []string{executable, "-c", configPath},
		"RunAtLoad":         true,
		"KeepAlive":         true,
		"ThrottleInterval":  10,
		"ProcessType":       "Background",
		"StandardOutPath":   "/var/log/jellyfin-remora.launchd.log",
		"StandardErrorPath": "/var/log/jellyfin-remora.launchd.err",
	}
	data, err := plist.MarshalIndent(payload, plist.XMLFormat, "  ")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(filepath.Dir(configPath), darwinServiceLabel+".plist")
	if err := atomicWriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	return &serviceArtifact{Kind: "launchd plist", Path: path}, nil
}

func platformServicePrivileged() bool { return os.Geteuid() == 0 }

func installPlatformService(artifact *serviceArtifact) error {
	data, err := os.ReadFile(artifact.Path)
	if err != nil {
		return err
	}
	destination := darwinInstalledServicePath()
	if err := atomicWriteFile(destination, data, 0o644); err != nil {
		return err
	}
	if err := darwinChown(destination, 0, 0); err != nil {
		return fmt.Errorf("set root:wheel ownership on %s: %w", destination, err)
	}
	return os.Chmod(destination, 0o644)
}

func startPlatformService(*serviceArtifact) error {
	domainTarget := "system/" + darwinServiceLabel
	if err := runDarwinLaunchctl("print", domainTarget); err == nil {
		if err := runDarwinLaunchctl("bootout", domainTarget); err != nil {
			return err
		}
	}
	return runDarwinLaunchctl("bootstrap", "system", darwinInstalledServicePath())
}

func platformServiceInstallInstructions(artifact *serviceArtifact) string {
	source := shellQuote(artifact.Path)
	destination := shellQuote(darwinInstalledServicePath())
	return strings.Join([]string{
		"Install it manually with:",
		"  sudo cp " + source + " " + destination,
		"  sudo chown root:wheel " + destination,
		"  sudo chmod 0644 " + destination,
		"  sudo launchctl bootout system/" + darwinServiceLabel + " 2>/dev/null || true",
		"  sudo launchctl bootstrap system " + destination,
	}, "\n")
}

func darwinInstalledServicePath() string {
	return filepath.Join(darwinLaunchDaemonDirectory, darwinServiceLabel+".plist")
}

func runLaunchctl(args ...string) error {
	output, err := exec.Command("/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
