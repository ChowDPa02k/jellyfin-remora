package main

import (
	"strings"
	"testing"
)

func TestSystemdPathValueEscapesControlCharacters(t *testing.T) {
	input := "/srv/remora\n[Service]\r\t" + string([]byte{0, 0x7f, 0xff}) + "\u0085 %\\config"
	want := `/srv/remora\x0a[Service]\x0d\x09\x00\x7f\xff\xc2\x85\x20%%\x5cconfig`
	got := systemdPathValue(input)
	if got != want {
		t.Fatalf("systemdPathValue = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "\n\r\t\x00\x7f") {
		t.Fatalf("escaped path retains an ASCII control character: %q", got)
	}
}
