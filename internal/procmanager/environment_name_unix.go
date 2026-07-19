//go:build !windows

package procmanager

func environmentNameEqual(left, right string) bool {
	return left == right
}
