//go:build windows

package config

func defaultPlatformControl(rest *RESTAPIConfig) {
	if rest.NamedPipe == "" {
		rest.NamedPipe = `\\.\pipe\jellyfin-remora`
	}
}
