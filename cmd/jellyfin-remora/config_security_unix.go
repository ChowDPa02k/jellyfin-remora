//go:build !windows

package main

import (
	"fmt"
	"os"
)

func configFileWarnings(path string) []string {
	st, err := os.Stat(path)
	if err != nil {
		return []string{fmt.Sprintf("cannot inspect configuration permissions: %v", err)}
	}
	if st.Mode().Perm()&0o077 != 0 {
		return []string{fmt.Sprintf("configuration mode %04o exposes credentials to group or others; use 0600", st.Mode().Perm())}
	}
	return nil
}
