//go:build !windows

package control

import "os"

func openLogForRead(path string) (*os.File, error) {
	return os.Open(path)
}
