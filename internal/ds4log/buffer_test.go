package ds4log

import (
	"bytes"
	"testing"
)

func TestBufferCapturesWrittenLines(t *testing.T) {
	b := NewBuffer(10)
	if _, err := b.Write([]byte("hello world\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := b.Lines()
	if len(got) != 1 || got[0] != "hello world" {
		t.Errorf("Lines() = %q, want [\"hello world\"]", got)
	}
	if n := b.Len(); n != 1 {
		t.Errorf("Len() = %d, want 1", n)
	}
}

func TestBufferSplitsMultilineWrite(t *testing.T) {
	b := NewBuffer(10)
	b.Write([]byte("a\nb\nc\n"))
	got := b.Lines()
	want := []string{"a", "b", "c"}
	if !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestBufferDropsOldestPastCap(t *testing.T) {
	b := NewBuffer(2)
	b.Write([]byte("one\n"))
	b.Write([]byte("two\n"))
	b.Write([]byte("three\n"))
	got := b.Lines()
	want := []string{"two", "three"}
	if !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q (capped to 2 most recent)", got, want)
	}
}

func TestBufferLinesSnapshotIsCopy(t *testing.T) {
	b := NewBuffer(10)
	b.Write([]byte("first\n"))
	snap := b.Lines()
	snap[0] = "mutated"
	if got := b.Lines(); got[0] != "first" {
		t.Errorf("internal slice mutated through snapshot: %q", got)
	}
}

func TestBufferTeeReceivesRawBytes(t *testing.T) {
	b := NewBuffer(10)
	var tee bytes.Buffer
	b.SetTee(&tee)
	b.Write([]byte("hello\nworld\n"))
	if got := tee.String(); got != "hello\nworld\n" {
		t.Errorf("tee = %q, want %q (tee receives the exact bytes)", got, "hello\nworld\n")
	}
	// And the ring still got the split lines.
	if got := b.Lines(); !equalStrings(got, []string{"hello", "world"}) {
		t.Errorf("ring after tee = %q, want [hello world]", got)
	}
}

func TestBufferIgnoresTrailingPartialLine(t *testing.T) {
	// libds4's go-side SetLogOutput delivers one full message per Write,
	// but defensively support partials: a write without a trailing newline
	// is buffered and joined to the next chunk.
	b := NewBuffer(10)
	b.Write([]byte("partial"))
	if n := b.Len(); n != 0 {
		t.Errorf("partial-only write produced %d line(s); want 0 until newline", n)
	}
	b.Write([]byte(" tail\n"))
	got := b.Lines()
	if !equalStrings(got, []string{"partial tail"}) {
		t.Errorf("after newline Lines() = %q, want [\"partial tail\"]", got)
	}
}

func TestBufferEmptyLineKept(t *testing.T) {
	// A bare "\n" is one (empty) line of output — keep it; libds4
	// occasionally emits blank separators between sections.
	b := NewBuffer(10)
	b.Write([]byte("\n"))
	if n := b.Len(); n != 1 {
		t.Errorf("Len() after blank line = %d, want 1", n)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
