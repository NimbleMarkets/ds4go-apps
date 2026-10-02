package main

import (
	"encoding/base64"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// A frame the terminal never consumed must not outlive the program: POSIX
// shared-memory objects persist until unlinked, and exit cancels the widget's
// own delayed cleanup. Linux exposes the objects as files, so it can be seen.
func TestShutdownReleasesUnconsumedSharedMemoryFrame(t *testing.T) {
	if !picture.KittySharedMemorySupported() {
		t.Skip("no shared-memory producer on this build")
	}
	m := kittyTransportModel(t, padui.KittyTransportSharedMemory)
	msg := m.presentPreview(image.NewRGBA(image.Rect(0, 0, 640, 480)))().(padui.FrameEncodedMsg)
	frame := msg.Msg.(picture.KittyFrameMsg)
	if frame.Medium != picture.KittyMediumSharedMemory {
		t.Fatalf("frame used %v, not shared memory", frame.Medium)
	}
	// The Kitty payload is the base64 object name.
	body := strings.TrimSuffix(strings.TrimPrefix(frame.APC, "\x1b_G"), "\x1b\\")
	_, payload, ok := strings.Cut(body, ";")
	if !ok {
		t.Fatalf("no payload in %q", frame.APC)
	}
	name, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload %q: %v", payload, err)
	}
	path := filepath.Join("/dev/shm", strings.TrimPrefix(string(name), "/"))
	next, _ := m.Update(msg) // submitted to a terminal that never reads it
	m = next.(model)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("submitted frame's object is already gone: %v", err)
	}
	m.shutdown()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("object %s survived shutdown (stat error: %v)", path, err)
	}
}
