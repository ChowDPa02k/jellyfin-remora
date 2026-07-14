//go:build windows

package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestWindowsEventLogFormatting(t *testing.T) {
	handler := &windowsEventLogHandler{
		base:  slog.NewJSONHandler(io.Discard, nil),
		attrs: []slog.Attr{slog.String("component", "storage")},
	}
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelError, "probe failed", 0)
	record.Add("target", `F:\`, "error", "access denied")
	message := handler.format(record)
	for _, expected := range []string{"probe failed", "component=storage", `target=F:\`, "error=access denied"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("formatted event %q omitted %q", message, expected)
		}
	}
}
