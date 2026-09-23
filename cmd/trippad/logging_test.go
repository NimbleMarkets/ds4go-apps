package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
)

func TestLanguageServerStderrLevels(t *testing.T) {
	const prefix = "2026-09-20T23:54:57.553518Z  "
	for _, tc := range []struct {
		name, output, want string
	}{
		{"initialized", prefix + "INFO wgsl_analyzer: Initialized\n", "INFO"},
		{"warning", prefix + "WARN wgsl_analyzer: slow analysis\n", "WARN"},
		{"error", prefix + "ERROR wgsl_analyzer: failed\n", "ERROR"},
		{"mixed", prefix + "INFO wgsl_analyzer: Initialized\n" + prefix + "ERROR wgsl_analyzer: failed\n", "ERROR"},
		{"unknown line", prefix + "INFO wgsl_analyzer: Initialized\npanic: failed\n", "ERROR"},
		{"untagged", "panic: unexpected failure\n", "ERROR"},
		{"partial", "wgsl_analyzer: Initialized\n", "ERROR"},
		{"not a timestamp", "something INFO wgsl_analyzer: failed\n", "ERROR"},
		{"blank", "\n", "ERROR"},
		{"debug", prefix + "DEBUG wgsl_analyzer: trace\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			h := shutdownLogHandler{Handler: slog.NewTextHandler(&out, nil), closing: new(atomic.Bool)}
			r := slog.NewRecord(time.Now(), slog.LevelError, "Language server stderr", 0)
			r.AddAttrs(slog.String("command", "/bin/wgsl-analyzer"), slog.String("output", tc.output))
			if err := h.Handle(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if out.Len() != 0 {
					t.Fatal("reclassified debug message ignored the configured threshold")
				}
			} else if !strings.Contains(out.String(), "level="+tc.want+" ") || !strings.Contains(out.String(), "command=/bin/wgsl-analyzer") {
				t.Fatal(out.String())
			}
			if r.Level != slog.LevelError {
				t.Fatal("mutated caller's record")
			}
		})
	}
}

func TestShutdownLogsFilterOnlyExpectedPipeClosure(t *testing.T) {
	// SetDefault also redirects the standard logger; restore both for other tests.
	previous, writer, flags := slog.Default(), log.Writer(), log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
	buffer := ds4log.NewBuffer(30)
	var file bytes.Buffer
	buffer.SetTee(&file)
	shutdown := configureGoLogs(buffer, false)
	closed := &os.PathError{Op: "read", Path: "|0", Err: os.ErrClosed}
	slog.Error("Error reading stderr", "error", closed, "case", "unexpected-before-shutdown")
	// Derived loggers must retain the same shutdown state and attributes.
	derived := slog.Default().With("component", "lsp").WithGroup("reader")
	shutdown()
	slog.Error("Error reading stderr", "error", closed, "case", "expected-shutdown")
	derived.Error("Error reading stderr", "error", closed, "case", "derived-shutdown")
	slog.Error("Error reading stderr", "error", errors.New("device I/O failure"), "case", "real-read-error")
	slog.Error("Error reading stderr", "error", closed.Error(), "case", "string-error")
	slog.Error("Different error", "error", closed, "case", "unrelated-error")
	derived.Error("Language server stderr", "output", "shader failure")
	log.Print("standard logger captured")
	slog.Debug("debug-disabled")
	for _, text := range []string{strings.Join(buffer.Lines(), "\n"), file.String()} {
		for _, want := range []string{"unexpected-before-shutdown", "real-read-error", "string-error", "unrelated-error", "shader failure", "component=lsp", "standard logger captured"} {
			if !strings.Contains(text, want) {
				t.Fatalf("lost useful diagnostic %q: %s", want, text)
			}
		}
		for _, unwanted := range []string{"case=expected-shutdown", "derived-shutdown", "debug-disabled"} {
			if strings.Contains(text, unwanted) {
				t.Fatalf("unwanted diagnostic %q: %s", unwanted, text)
			}
		}
	}
}
