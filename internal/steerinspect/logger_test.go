package steerinspect

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestLogBufferBasic(t *testing.T) {
	tmp, err := os.MkdirTemp("", "log-buffer-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmp)

	logFilePath := filepath.Join(tmp, "test.log")

	// Limit to last 3 lines
	buf, err := NewLogBuffer(logFilePath, 3)
	if err != nil {
		t.Fatalf("failed to create log buffer: %v", err)
	}
	defer buf.Close()

	buf.WriteLog(0, "line 1\n")
	buf.WriteLog(0, "line 2\r\n")
	buf.WriteLog(0, "line 3\nline 4\n")

	// In memory, we should have the last 3 lines: "line 2", "line 3", "line 4"
	lines := buf.GetLines()
	expectedLines := []string{"line 2", "line 3", "line 4"}
	if len(lines) != len(expectedLines) {
		t.Fatalf("expected %d lines, got %d", len(expectedLines), len(lines))
	}
	for i, expected := range expectedLines {
		if lines[i] != expected {
			t.Errorf("line %d: expected %q, got %q", i, expected, lines[i])
		}
	}

	// Verify file content
	fileBytes, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	expectedFileContent := "line 1\nline 2\r\nline 3\nline 4\n"
	if string(fileBytes) != expectedFileContent {
		t.Errorf("expected file content %q, got %q", expectedFileContent, string(fileBytes))
	}
}

func TestLogBufferConcurrency(t *testing.T) {
	buf, err := NewLogBuffer("", 100)
	if err != nil {
		t.Fatalf("failed to create log buffer: %v", err)
	}
	defer buf.Close()

	var wg sync.WaitGroup
	workers := 10
	iterations := 50

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				buf.WriteLog(0, "worker "+strconv.Itoa(workerID)+" iter "+strconv.Itoa(i)+"\n")
			}
		}(w)
	}

	wg.Wait()

	// Should run successfully without race issues.
	lines := buf.GetLines()
	if len(lines) != 100 {
		t.Errorf("expected exactly 100 lines, got %d", len(lines))
	}
}
