//go:build darwin

package procmanager

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty/v2"
	"golang.org/x/term"
)

func configureChildConsole(cmd *exec.Cmd, output io.Writer, _ io.Writer) (*childConsole, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}
	if _, err := term.MakeRaw(int(tty.Fd())); err != nil {
		_ = ptmx.Close()
		_ = tty.Close()
		return nil, err
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 160}); err != nil {
		_ = ptmx.Close()
		_ = tty.Close()
		return nil, err
	}
	cmd.Stdout = tty
	cmd.Stderr = tty
	done := make(chan error, 1)
	return &childConsole{
		started: func() {
			_ = tty.Close()
			go func() {
				_, copyErr := io.Copy(output, ptmx)
				_ = ptmx.Close()
				done <- normalizePTYError(copyErr)
				close(done)
			}()
		},
		abort: func() {
			_ = tty.Close()
			_ = ptmx.Close()
		},
		finish: func() error {
			select {
			case copyErr := <-done:
				_ = ptmx.Close()
				return copyErr
			case <-time.After(time.Second):
				_ = ptmx.Close()
				<-done
				return nil
			}
		},
	}, nil
}

func normalizePTYError(err error) error {
	if err == nil || errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
