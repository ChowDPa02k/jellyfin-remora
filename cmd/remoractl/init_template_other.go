//go:build !windows

package main

import (
	"fmt"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func preparePlatformTemplate(template []byte, requestedVolume, requestedDataRoot string) ([]byte, error) {
	if requestedVolume != "" {
		return nil, fmt.Errorf("--volume is supported only on Windows")
	}
	if requestedDataRoot != "" {
		return nil, fmt.Errorf("--data-root is supported only on Windows")
	}
	return template, nil
}

func preparePlatformInitDirectories(*config.Config) error { return nil }
