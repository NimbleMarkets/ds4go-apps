package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// powernap v0.1.6 closes its stderr pipe during Exit/Kill while its reader is
// still running. Its reader logs os.ErrClosed as an error. Suppress only that
// known record once the host has started shutdown; preserve all other errors.
type shutdownLogHandler struct {
	slog.Handler
	closing *atomic.Bool
}

func (h shutdownLogHandler) Handle(ctx context.Context, record slog.Record) error {
	// powernap labels every stderr chunk ERROR. Honor explicit timestamped
	// server levels, retaining the highest level if a read contains several
	// lines. Unknown or partial lines keep the original level.
	if record.Message == "Language server stderr" {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "output" && attr.Value.Resolve().Kind() == slog.KindString {
				record.Level = serverStderrLevel(attr.Value.Resolve().String(), record.Level)
				return false
			}
			return true
		})
		if !h.Handler.Enabled(ctx, record.Level) {
			return nil
		}
	}
	if h.closing.Load() && record.Message == "Error reading stderr" {
		closed := false
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "error" {
				err, _ := attr.Value.Resolve().Any().(error)
				closed = errors.Is(err, os.ErrClosed)
				return false
			}
			return true
		})
		if closed {
			return nil
		}
	}
	return h.Handler.Handle(ctx, record)
}

func serverStderrLevel(output string, fallback slog.Level) slog.Level {
	level, found := slog.LevelDebug, false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 3 {
			return fallback
		}
		if _, err := time.Parse(time.RFC3339Nano, fields[0]); err != nil {
			return fallback
		}
		var current slog.Level
		switch fields[1] {
		case "TRACE", "DEBUG":
			current = slog.LevelDebug
		case "INFO":
			current = slog.LevelInfo
		case "WARN":
			current = slog.LevelWarn
		case "ERROR":
			current = slog.LevelError
		default:
			return fallback
		}
		level, found = max(level, current), true
	}
	if !found {
		return fallback
	}
	return level
}

func (h shutdownLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return shutdownLogHandler{Handler: h.Handler.WithAttrs(attrs), closing: h.closing}
}

func (h shutdownLogHandler) WithGroup(name string) slog.Handler {
	return shutdownLogHandler{Handler: h.Handler.WithGroup(name), closing: h.closing}
}

// configureGoLogs gives process-global dependency logs the same sink as native
// diagnostics. Keep it installed through process exit: subprocess reader logs
// can arrive after Close returns. The returned function marks expected shutdown.
func configureGoLogs(output io.Writer, debug bool) func() {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	closing := new(atomic.Bool)
	slog.SetDefault(slog.New(shutdownLogHandler{
		Handler: slog.NewTextHandler(output, &slog.HandlerOptions{Level: level}),
		closing: closing,
	}))
	return func() { closing.Store(true) }
}
