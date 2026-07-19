package main

import (
	"testing"
	"unicode/utf8"
)

func TestShortIDTruncatesAtRuneBoundary(t *testing.T) {
	got := shortID("会话标识一二三四五")
	if want := "会话标识一二三四"; got != want {
		t.Fatalf("shortID = %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("shortID returned invalid UTF-8: %q", got)
	}
	if got := shortID("a1b2c3d4-extra"); got != "a1b2c3d4" {
		t.Fatalf("ASCII shortID = %q, want %q", got, "a1b2c3d4")
	}
}
