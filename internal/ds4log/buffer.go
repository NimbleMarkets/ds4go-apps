// Package ds4log captures libds4 diagnostic output for in-app display and
// optional file teeing. A Buffer plugs straight into ds4.SetLogOutput
// because it satisfies io.Writer; both the glyphpad and svgpad TUIs use it
// to power the ctrl+n log overlay, optionally teeing to a debug file.
package ds4log

import (
	"bytes"
	"io"
	"sync"
)

// Buffer is a bounded, line-oriented capture of libds4 log output. It is
// safe for concurrent writes because libds4 may invoke the log callback
// from native worker threads. Writes are split on '\n'; an unterminated
// trailing chunk is held until a subsequent write supplies the newline.
type Buffer struct {
	mu      sync.Mutex
	lines   []string
	cap     int       // 0 means unbounded
	partial []byte    // accumulates bytes after the last '\n'
	tee     io.Writer // optional pass-through (e.g. a debug log file)
}

// NewBuffer returns a Buffer that retains at most cap completed lines.
// cap <= 0 means unbounded.
func NewBuffer(cap int) *Buffer { return &Buffer{cap: cap} }

// SetTee installs (or clears, with nil) an io.Writer that receives the
// exact bytes passed to Write before any line-splitting. A non-nil tee is
// the file-debug sink referenced by the spec.
func (b *Buffer) SetTee(w io.Writer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tee = w
}

// Write appends bytes to the buffer, splitting on '\n'. The tee (if any)
// receives the original bytes, untouched. Tee errors are returned so
// io.Writer semantics are honored; libds4's wrapper ignores them, so the
// only downstream effect is that the debug file may drop bytes silently.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var teeErr error
	if b.tee != nil {
		if _, err := b.tee.Write(p); err != nil {
			teeErr = err
		}
	}

	b.partial = append(b.partial, p...)
	for {
		i := bytes.IndexByte(b.partial, '\n')
		if i < 0 {
			break
		}
		b.appendLine(string(b.partial[:i]))
		b.partial = b.partial[i+1:]
	}
	return len(p), teeErr
}

// appendLine adds one completed line, dropping the oldest if at cap. The
// oldest entries are copied off to a fresh slice so prior snapshots from
// Lines() remain intact.
func (b *Buffer) appendLine(line string) {
	if b.cap > 0 && len(b.lines) >= b.cap {
		dropped := len(b.lines) - b.cap + 1
		newLines := make([]string, 0, b.cap)
		newLines = append(newLines, b.lines[dropped:]...)
		b.lines = newLines
	}
	b.lines = append(b.lines, line)
}

// Lines returns a snapshot of the captured lines. Callers may mutate the
// returned slice freely; the internal store is unaffected.
func (b *Buffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}

// Len returns the number of completed lines currently held.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

// Compile-time check: Buffer is an io.Writer, so it plugs straight into
// ds4.SetLogOutput.
var _ io.Writer = (*Buffer)(nil)
