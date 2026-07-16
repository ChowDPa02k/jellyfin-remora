//go:build windows

package config

import "github.com/ChowDPa02K/jellyfin-remora/internal/contract"

func defaultPlatformControl(rest *RESTAPIConfig) {
	if rest.NamedPipe == "" {
		rest.NamedPipe = contract.WindowsNamedPipe
	}
}
