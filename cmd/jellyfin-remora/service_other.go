//go:build !windows

package main

import (
	"context"
	"errors"
)

func runPlatformService(func(context.Context) error) error {
	return errors.New("--service is supported only on Windows")
}
