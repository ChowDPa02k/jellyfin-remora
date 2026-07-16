//go:build !darwin && !linux && !windows

package main

import (
	"io"
	"os"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func acquireInstanceLock(*config.Config) (io.Closer, error) {
	return os.Open(os.DevNull)
}
