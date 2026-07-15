//go:build !darwin

package procmanager

import (
	"io"
	"os/exec"
)

func configureChildConsole(cmd *exec.Cmd, stdout, stderr io.Writer) (*childConsole, error) {
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return &childConsole{
		started: func() {},
		abort:   func() {},
		finish:  func() error { return nil },
	}, nil
}
