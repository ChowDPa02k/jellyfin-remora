//go:build windows

package main

import "os"

func replaceConfigurationFile(path string, data []byte, mode os.FileMode, _ os.FileInfo) error {
	return atomicWriteFile(path, data, mode)
}
