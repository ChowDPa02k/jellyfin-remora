//go:build windows

package platform

import "testing"

func TestWindowsSMBCredentialTarget(t *testing.T) {
	for input, want := range map[string]string{
		`//192.168.1.20/share`: `192.168.1.20`,
		`\\NAS.local\media`:    `NAS.local`,
		`nas./share`:           `nas`,
	} {
		if got := windowsSMBCredentialTarget(input); got != want {
			t.Errorf("windowsSMBCredentialTarget(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWindowsSMBCredentialCandidates(t *testing.T) {
	got := windowsSMBCredentialCandidates("nas")
	want := []string{"nas", "Domain:target=nas", "LegacyGeneric:target=nas"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("candidate %d = %q, want %q", index, got[index], want[index])
		}
	}
}
