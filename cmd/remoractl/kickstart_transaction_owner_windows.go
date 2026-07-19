//go:build windows

package main

import "os"

func captureKickstartPathOwner(os.FileInfo) any   { return nil }
func restoreKickstartPathOwner(string, any) error { return nil }
