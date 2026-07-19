//go:build windows

package main

import "os"

func temporaryFileSignals() []os.Signal { return []os.Signal{os.Interrupt} }

func terminateAfterTemporaryFileCleanup(os.Signal) { os.Exit(1) }
