//go:build linux

package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

func validatePlatformConfig(c *Config) error {
	if c.RESTAPI.NamedPipe != "" {
		return fmt.Errorf("restapi.named-pipe is only supported on Windows")
	}
	if c.Jellyfin.RunAsUser == "root" || c.Jellyfin.RunAsUser == "0" {
		return fmt.Errorf("refusing to run Jellyfin as root")
	}
	return nil
}

func validatePlatformDisk(index int, disk DiskConfig) error {
	if disk.VolumeGUID != "" || disk.VolumeLabel != "" || disk.Filesystem != "" {
		return fmt.Errorf("disk[%d] volume-guid, volume-label, and filesystem are only supported on Windows", index)
	}
	if disk.Type == "smb" {
		if disk.User != "" || disk.Password != "" {
			return fmt.Errorf("disk[%d] rejects SMB user/password in YAML; use credential: file:/absolute/path", index)
		}
		if disk.Credential != "" {
			credential := strings.TrimPrefix(disk.Credential, "file:")
			if credential == disk.Credential && strings.HasPrefix(disk.Credential, "libsecret:") {
				if strings.TrimSpace(strings.TrimPrefix(disk.Credential, "libsecret:")) == "" {
					return fmt.Errorf("disk[%d].credential libsecret key is empty", index)
				}
				return nil
			}
			if !filepath.IsAbs(credential) {
				return fmt.Errorf("disk[%d].credential file path must be absolute", index)
			}
		}
	} else if disk.Credential != "" {
		return fmt.Errorf("disk[%d].credential is only valid for SMB disks", index)
	}
	return nil
}
