//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

const linuxServiceName = "jellyfin-remora.service"

var linuxSystemdDirectory = "/etc/systemd/system"

func platformSampleName() (string, error) { return "config-linux.yaml", nil }
func remoraExecutableName() string        { return "jellyfin-remora" }

func generatePlatformService(_ *config.Config, executable, configPath string) (*serviceArtifact, error) {
	unit := `[Unit]
Description=Jellyfin Remora
After=network-online.target remote-fs.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=` + systemdQuote(executable) + ` -c ` + systemdQuote(configPath) + `
Restart=on-failure
RestartSec=10s
TimeoutStopSec=330s

[Install]
WantedBy=multi-user.target
`
	path := filepath.Join(filepath.Dir(configPath), linuxServiceName)
	if err := atomicWriteFile(path, []byte(unit), 0o644); err != nil {
		return nil, err
	}
	return &serviceArtifact{Kind: "systemd service", Path: path}, nil
}

func platformServicePrivileged() bool { return os.Geteuid() == 0 }

func installPlatformService(artifact *serviceArtifact) error {
	data, err := os.ReadFile(artifact.Path)
	if err != nil {
		return err
	}
	destination := filepath.Join(linuxSystemdDirectory, linuxServiceName)
	if err := atomicWriteFile(destination, data, 0o644); err != nil {
		return err
	}
	if err := os.Chown(destination, 0, 0); err != nil {
		return err
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	return runSystemctl("enable", linuxServiceName)
}

func startPlatformService(*serviceArtifact) error {
	return runSystemctl("start", linuxServiceName)
}

func platformServiceInstallInstructions(artifact *serviceArtifact) string {
	source := shellQuote(artifact.Path)
	destination := shellQuote(filepath.Join(linuxSystemdDirectory, linuxServiceName))
	return strings.Join([]string{
		"Install it manually with:",
		"  sudo cp " + source + " " + destination,
		"  sudo chown root:root " + destination,
		"  sudo chmod 0644 " + destination,
		"  sudo systemctl daemon-reload",
		"  sudo systemctl enable " + linuxServiceName,
		"  sudo systemctl start " + linuxServiceName,
	}, "\n")
}

func runSystemctl(args ...string) error {
	output, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func systemdQuote(value string) string {
	return strconv.Quote(strings.ReplaceAll(value, "%", "%%"))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
