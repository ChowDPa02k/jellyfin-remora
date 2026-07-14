//go:build darwin

package config

import "fmt"

func validatePlatformConfig(c *Config) error {
	if c.RESTAPI.NamedPipe != "" {
		return fmt.Errorf("restapi.named-pipe is only supported on Windows")
	}
	if c.Jellyfin.RunAsUser == "root" {
		return fmt.Errorf("refusing to run Jellyfin as root")
	}
	return nil
}

func validatePlatformDisk(index int, disk DiskConfig) error {
	if disk.Type == "physical" && disk.VolumeGUID != "" {
		return fmt.Errorf("disk[%d].volume-guid is only supported on Windows", index)
	}
	if disk.Credential != "" {
		return fmt.Errorf("disk[%d].credential is only supported on Windows", index)
	}
	return nil
}
