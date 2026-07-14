//go:build windows

package main

import (
	"os"
	"os/exec"
)

func childCommand() *exec.Cmd {
	return exec.Command(os.Args[0], "--child=true")
}
