//go:build !darwin

package procmanager

func platformDefaultWebDir(string) (string, error) { return "", nil }
