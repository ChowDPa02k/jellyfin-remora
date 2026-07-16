//go:build !darwin && !linux && !windows

package kickstart

func platformInstallCandidates() []string   { return nil }
func platformWebCandidates(string) []string { return nil }
