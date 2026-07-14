//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func TestGenerateWindowsServiceInstaller(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Jellyfin.ConfigDir = dir
	cfg.Remora.DataDir = filepath.Join(dir, "state")
	artifact, err := generatePlatformService(cfg, `C:\Program Files\Remora\jellyfin-remora.exe`, filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if artifact == nil || artifact.Kind != "Windows service installer" {
		t.Fatalf("artifact = %#v", artifact)
	}
	data, err := os.ReadFile(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{"#Requires -RunAsAdministrator", "--service -c", "NT SERVICE\\JellyfinRemora", "New-Service", "Set-ServiceIdentity", "Invoke-CimMethod", "failureflag", "Start-Service", "InstallTask", "Stop-ScheduledTask", "Register-ScheduledTask", "Uninstall it before installing the service", "New-EventLog", "$installDir", "(OI)(CI)RX", "Grant-ServiceLogonRight", "LsaAddAccountRights", "SeServiceLogonRight"} {
		if !strings.Contains(script, required) {
			t.Fatalf("installer omitted %q", required)
		}
	}
	pathLiteral := strings.ReplaceAll(artifact.Path, "'", "''")
	parse := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"$path='"+pathLiteral+"'; $tokens=$null; $errors=$null; [System.Management.Automation.Language.Parser]::ParseFile($path,[ref]$tokens,[ref]$errors) | Out-Null; if($errors.Count){$errors | ForEach-Object {$_.Message}; exit 1}")
	if output, err := parse.CombinedOutput(); err != nil {
		t.Fatalf("generated installer has invalid PowerShell syntax: %v\n%s", err, output)
	}
}
