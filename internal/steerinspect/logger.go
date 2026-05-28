package steerinspect

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// LogBuffer is a thread-safe log accumulator that writes logs to a file
// and keeps the last N lines in memory for an internal viewer.
type LogBuffer struct {
	mu     sync.Mutex
	file   *os.File
	lines  []string
	maxLen int
}

// NewLogBuffer initializes a LogBuffer. If filePath is empty, file logging is disabled.
func NewLogBuffer(filePath string, maxLen int) (*LogBuffer, error) {
	var f *os.File
	var err error
	if filePath != "" {
		f, err = os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file %s: %w", filePath, err)
		}
	}
	if maxLen <= 0 {
		maxLen = 500
	}
	return &LogBuffer{
		file:   f,
		lines:  make([]string, 0, maxLen),
		maxLen: maxLen,
	}, nil
}

// WriteLog writes a log message to the log file and appends it to the in-memory line buffer.
func (l *LogBuffer) WriteLog(typ ds4api.LogType, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		_, _ = l.file.WriteString(msg)
	}

	trimmed := strings.TrimRight(msg, "\r\n")
	if trimmed == "" {
		return
	}
	parts := strings.Split(trimmed, "\n")
	for _, part := range parts {
		l.lines = append(l.lines, part)
	}
	if len(l.lines) > l.maxLen {
		l.lines = l.lines[len(l.lines)-l.maxLen:]
	}
}

// GetLines returns a copy of the accumulated log lines.
func (l *LogBuffer) GetLines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	res := make([]string, len(l.lines))
	copy(res, l.lines)
	return res
}

// Close closes the underlying log file if it is open.
func (l *LogBuffer) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}
