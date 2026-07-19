//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func childCommand() *exec.Cmd {
	cmd := exec.Command("/bin/sleep", "300")
	// Exercise the supervisor's descendant walk rather than relying on the
	// parent's process-group signal to terminate this child.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
