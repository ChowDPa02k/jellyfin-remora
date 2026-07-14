//go:build !darwin && !windows

package config

func validatePlatformConfig(*Config) error       { return nil }
func validatePlatformDisk(int, DiskConfig) error { return nil }
