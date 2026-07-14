//go:build !windows

package config

import "fmt"

func defaultPlatformControl(rest *RESTAPIConfig) {
	if rest.UnixSocket == "" {
		rest.UnixSocket = fmt.Sprintf("/tmp/.s.remora.%d", rest.Port)
	}
}
