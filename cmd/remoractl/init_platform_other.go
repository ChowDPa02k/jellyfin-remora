//go:build !darwin && !linux && !windows

package main

import (
	"fmt"
	"runtime"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
)

func platformSampleName() (string, error) {
	return "", fmt.Errorf("remoractl init does not support %s", runtime.GOOS)
}

func remoraExecutableName() string { return "jellyfin-remora" }

func generatePlatformService(*config.Config, string, string) (*serviceArtifact, error) {
	return nil, fmt.Errorf("service generation is unsupported on %s", runtime.GOOS)
}

func platformServicePrivileged() bool { return false }

func installPlatformService(*serviceArtifact) error {
	return fmt.Errorf("service installation is unsupported on %s", runtime.GOOS)
}

func startPlatformService(*serviceArtifact) error {
	return fmt.Errorf("service startup is unsupported on %s", runtime.GOOS)
}

func platformServiceInstallInstructions(*serviceArtifact) string {
	return fmt.Sprintf("Install the generated service definition manually on %s.", runtime.GOOS)
}
