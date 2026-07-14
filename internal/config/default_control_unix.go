//go:build !windows

package config

import (
	"os"
	"path/filepath"
)

func defaultPlatformControl(rest *RESTAPIConfig) {
	if rest.UnixSocket == "" {
		rest.UnixSocket = filepath.Join(os.TempDir(), "jellyfin-remora.sock")
	}
}
