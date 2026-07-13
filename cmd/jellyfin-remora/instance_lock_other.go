//go:build !darwin

package main

import (
	"io"
	"os"
)

func acquireInstanceLock(string) (io.Closer, error) {
	return os.Open(os.DevNull)
}
