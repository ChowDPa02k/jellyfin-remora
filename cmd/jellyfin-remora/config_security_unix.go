//go:build !windows

package main

import (
	"fmt"
	"os"
)

func validateConfigFileSecurity(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect configuration permissions: %w", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("configuration mode %04o exposes credentials to group or others; use an owner-only mode such as 0600", st.Mode().Perm())
	}
	return nil
}

func configFileWarnings(string) []string { return nil }
