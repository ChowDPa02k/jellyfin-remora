//go:build windows

package procmanager

import "os"

func platformExecutableModeOK(os.FileMode) bool { return true }

func platformExecutableCandidates() []string { return []string{"jellyfin.exe"} }
