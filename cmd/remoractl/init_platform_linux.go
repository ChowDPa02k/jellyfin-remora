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
	"github.com/ChowDPa02K/jellyfin-remora/internal/contract"
)

const linuxServiceName = contract.LinuxServiceName

var linuxSystemdDirectory = "/etc/systemd/system"

var (
	linuxChown      = os.Chown
	runLinuxSystemd = runSystemctl
)

func platformSampleName() (string, error) { return "config-linux.yaml", nil }
func remoraExecutableName() string        { return "jellyfin-remora" }

func generatePlatformService(_ *config.Config, executable, configPath string) (*serviceArtifact, error) {
	unit := `[Unit]
Description=Jellyfin Remora
Documentation=https://github.com/ChowDPa02K/jellyfin-remora
ConditionPathExists=` + systemdPathValue(configPath) + `
After=network-online.target local-fs.target remote-fs.target jellyfin.service
Wants=network-online.target remote-fs.target
Conflicts=jellyfin.service
Before=umount.target shutdown.target
StartLimitIntervalSec=5min
StartLimitBurst=5

[Service]
Type=simple
ExecStart=` + systemdQuote(executable) + ` -c ` + systemdQuote(configPath) + `
Restart=on-failure
RestartSec=10s
TimeoutStopSec=330s
# Remora owns the Jellyfin process tree. Preserve it across an unexpected
# Remora crash so the restarted supervisor can adopt the exact process.
KillMode=process
RuntimeDirectory=jellyfin-remora
RuntimeDirectoryMode=0750
StateDirectory=jellyfin-remora
StateDirectoryMode=0750
LogsDirectory=jellyfin-remora
LogsDirectoryMode=0750
UMask=0027
LimitNOFILE=65536

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
	if err := linuxChown(destination, 0, 0); err != nil {
		return err
	}
	if err := runLinuxSystemd("daemon-reload"); err != nil {
		return err
	}
	return runLinuxSystemd("enable", linuxServiceName)
}

func startPlatformService(*serviceArtifact) error {
	if err := runLinuxSystemd("reset-failed", linuxServiceName); err != nil {
		return err
	}
	return runLinuxSystemd("restart", linuxServiceName)
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
