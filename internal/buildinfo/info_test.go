package buildinfo

import (
	"strings"
	"testing"
)

func TestCurrentIncludesInjectedMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, Commit, Date
	Version, Commit, Date = "v1.2.3", "abc123", "2026-07-13T00:00:00Z"
	t.Cleanup(func() { Version, Commit, Date = oldVersion, oldCommit, oldDate })

	got := Current("remora").String()
	for _, want := range []string{"remora v1.2.3", "abc123", "2026-07-13T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Fatalf("String() = %q, want %q", got, want)
		}
	}
}
