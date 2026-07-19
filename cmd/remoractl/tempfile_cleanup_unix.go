//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func temporaryFileSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func terminateAfterTemporaryFileCleanup(received os.Signal) {
	signal.Reset(received)
	if unixSignal, ok := received.(syscall.Signal); ok {
		_ = syscall.Kill(os.Getpid(), unixSignal)
	}
	os.Exit(1)
}
