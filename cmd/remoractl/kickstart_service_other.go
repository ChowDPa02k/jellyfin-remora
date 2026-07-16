//go:build !linux

package main

func prepareKickstartServiceExecutable(remoraExecutable string) (string, error) {
	return remoraExecutable, nil
}
