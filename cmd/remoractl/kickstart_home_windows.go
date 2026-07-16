//go:build windows

package main

import (
	"fmt"
	"os"

	"github.com/ChowDPa02K/jellyfin-remora/internal/kickstart"
)

func prepareKickstartHome(answers kickstart.Answers) error {
	if err := os.MkdirAll(answers.Home, 0o750); err != nil {
		return fmt.Errorf("create Jellyfin home: %w", err)
	}
	return nil
}
