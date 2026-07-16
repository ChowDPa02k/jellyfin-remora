//go:build !darwin && !linux

package procmanager

func platformDefaultWebDir(string) (string, error) { return "", nil }
