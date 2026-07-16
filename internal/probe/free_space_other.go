//go:build !windows && !linux

package probe

func ensureWriteCapacity(string, uint64) error {
	return nil
}
