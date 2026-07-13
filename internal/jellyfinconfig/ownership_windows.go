//go:build windows

package jellyfinconfig

func preserveOwnership(_, _ string) error { return nil }
