//go:build windows

package probe

import (
	"math"
	"strings"
	"testing"
)

func TestEnsureWriteCapacityRejectsUnavailableSpace(t *testing.T) {
	err := ensureWriteCapacity(t.TempDir(), math.MaxUint64)
	if err == nil || !strings.Contains(err.Error(), "insufficient free space") {
		t.Fatalf("ensureWriteCapacity error = %v", err)
	}
}
