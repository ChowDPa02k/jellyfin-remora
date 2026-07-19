//go:build windows

package procmanager

import "strings"

func environmentNameEqual(left, right string) bool {
	return strings.EqualFold(left, right)
}
