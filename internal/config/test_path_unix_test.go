//go:build !windows

package config

func testSMBTarget(root string) string { return root + "/share" }
