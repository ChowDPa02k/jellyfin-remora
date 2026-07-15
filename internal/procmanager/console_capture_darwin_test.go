//go:build darwin

package procmanager

import (
	"bytes"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestDarwinConsoleCaptureProvidesRawTTYAndANSI(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", `if [ -t 1 ]; then printf '\033[32mstdout\033[0m\n'; printf '\033[31mstderr\033[0m\n' >&2; else exit 42; fi`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	capture, err := configureChildConsole(cmd, &output, &output)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		capture.abort()
		t.Fatal(err)
	}
	capture.started()
	waitErr := cmd.Wait()
	captureErr := capture.finish()
	if waitErr != nil || captureErr != nil {
		t.Fatalf("wait=%v capture=%v output=%q", waitErr, captureErr, output.String())
	}
	got := output.String()
	if !strings.Contains(got, "\x1b[32mstdout\x1b[0m\n") || !strings.Contains(got, "\x1b[31mstderr\x1b[0m\n") {
		t.Fatalf("PTY did not preserve ANSI output: %q", got)
	}
	if strings.Contains(got, "\r\n") {
		t.Fatalf("PTY changed line endings despite raw mode: %q", got)
	}
}
