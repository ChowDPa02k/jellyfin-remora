//go:build !windows

package procmanager

import "os"

func platformExecutableModeOK(mode os.FileMode) bool { return mode&0o111 != 0 }

func platformExecutableCandidates() []string { return []string{"Jellyfin", "jellyfin"} }
