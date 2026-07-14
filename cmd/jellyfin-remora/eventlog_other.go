//go:build !windows

package main

import (
	"io"
	"log/slog"
)

func withPlatformLogging(handler slog.Handler) (slog.Handler, io.Closer) {
	return handler, nil
}
