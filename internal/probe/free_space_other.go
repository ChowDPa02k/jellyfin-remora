//go:build !windows

package probe

func ensureWriteCapacity(string, uint64) error {
	return nil
}
