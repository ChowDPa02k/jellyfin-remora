//go:build !windows

package main

import "os/exec"

func childCommand() *exec.Cmd {
	return exec.Command("/bin/sleep", "300")
}
