//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"golang.org/x/sys/windows/svc/eventlog"
)

type windowsEventLogHandler struct {
	base   slog.Handler
	log    *eventlog.Log
	attrs  []slog.Attr
	groups []string
}

func withPlatformLogging(handler slog.Handler) (slog.Handler, io.Closer) {
	log, err := eventlog.Open(windowsServiceName)
	if err != nil {
		return handler, nil
	}
	return &windowsEventLogHandler{base: handler, log: log}, log
}

func (h *windowsEventLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *windowsEventLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if err := h.base.Handle(ctx, record); err != nil {
		return err
	}
	if record.Level < slog.LevelWarn {
		return nil
	}
	message := h.format(record)
	if record.Level >= slog.LevelError {
		return h.log.Error(1, message)
	}
	return h.log.Warning(2, message)
}

func (h *windowsEventLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.base = h.base.WithAttrs(attrs)
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *windowsEventLogHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.base = h.base.WithGroup(name)
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

func (h *windowsEventLogHandler) format(record slog.Record) string {
	var fields []string
	appendAttr := func(attr slog.Attr) {
		attr.Value = attr.Value.Resolve()
		key := attr.Key
		if len(h.groups) > 0 {
			key = strings.Join(h.groups, ".") + "." + key
		}
		fields = append(fields, fmt.Sprintf("%s=%v", key, attr.Value.Any()))
	}
	for _, attr := range h.attrs {
		appendAttr(attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		appendAttr(attr)
		return true
	})
	if len(fields) == 0 {
		return record.Message
	}
	return record.Message + " | " + strings.Join(fields, " ")
}
