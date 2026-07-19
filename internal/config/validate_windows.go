//go:build windows

package config

import (
	"fmt"
	"regexp"
	"strings"
)

var windowsVolumeGUIDPattern = regexp.MustCompile(`(?i)^\\\\\?\\Volume\{[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\}\\$`)

func validatePlatformConfig(c *Config) error {
	if c.RESTAPI.UnixSocket != "" {
		return fmt.Errorf("restapi.unix-socket is not supported on Windows")
	}
	if !strings.HasPrefix(strings.ToLower(c.RESTAPI.NamedPipe), `\\.\pipe\`) {
		return fmt.Errorf("restapi.named-pipe must use \\\\.\\pipe\\name form")
	}
	seenEnvironmentNames := make(map[string]string, len(c.Jellyfin.Env))
	for name := range c.Jellyfin.Env {
		folded := strings.ToLower(name)
		if previous, exists := seenEnvironmentNames[folded]; exists {
			return fmt.Errorf("jellyfin.env contains case-colliding Windows variable names %q and %q", previous, name)
		}
		seenEnvironmentNames[folded] = name
	}
	return nil
}

func validatePlatformDisk(index int, disk DiskConfig) error {
	if disk.Type == "physical" {
		if disk.VolumeGUID == "" {
			return fmt.Errorf("disk[%d] physical disk requires volume-guid on Windows", index)
		}
		if !windowsVolumeGUIDPattern.MatchString(disk.VolumeGUID) {
			return fmt.Errorf("disk[%d].volume-guid must use \\\\?\\Volume{GUID}\\ form", index)
		}
	}
	if disk.Credential != "" && !strings.EqualFold(disk.Credential, "windows-credential-manager") {
		return fmt.Errorf("disk[%d].credential must be windows-credential-manager", index)
	}
	if disk.Type == "smb" && (disk.User != "" || disk.Password != "") {
		return fmt.Errorf("disk[%d] Windows SMB credentials must be stored under the service identity or in Windows Credential Manager, not YAML user/password", index)
	}
	if disk.Type == "smb" || disk.Type == "nfs" {
		if !regexp.MustCompile(`(?i)^[a-z]:\\$`).MatchString(disk.Target) {
			return fmt.Errorf("disk[%d].target must be a Windows drive root such as F:\\ for %s", index, disk.Type)
		}
	}
	if disk.Type == "nfs" {
		if disk.User != "" || disk.Password != "" || disk.Credential != "" {
			return fmt.Errorf("disk[%d] Windows NFS credentials must be configured through Client for NFS or Kerberos options, not YAML", index)
		}
		source := strings.TrimSpace(strings.ReplaceAll(disk.Device, `\`, "/"))
		if !strings.Contains(source, ":/") && !strings.HasPrefix(source, "//") {
			return fmt.Errorf("disk[%d].device must use server:/share or //server/share form for Windows NFS", index)
		}
	}
	return nil
}
