package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui/padtest"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// sharedObjectPath returns where Linux exposes the object a shared-memory
// frame refers to. The Kitty payload is the base64 object name.
func sharedObjectPath(t *testing.T, apc string) string {
	t.Helper()
	body := strings.TrimSuffix(strings.TrimPrefix(apc, "\x1b_G"), "\x1b\\")
	_, payload, ok := strings.Cut(body, ";")
	if !ok {
		t.Fatalf("no payload in %q", apc)
	}
	name, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload %q: %v", payload, err)
	}
	return filepath.Join("/dev/shm", strings.TrimPrefix(string(name), "/"))
}

// A frame the terminal never consumed must not outlive the program: POSIX
// shared-memory objects persist until unlinked, and exit cancels the widget's
// own delayed cleanup.
func TestCloseReleasesUnconsumedSharedMemoryFrame(t *testing.T) {
	if !picture.KittySharedMemorySupported() {
		t.Skip("no shared-memory producer on this build")
	}
	previous := picture.KittySupported()
	picture.ForceKittyCapability(picture.KittyCapabilitySupported)
	padtest.AllowKittySharedMemory(t)
	t.Cleanup(func() { picture.ForceKittyCapability(previous) })
	m := testModel(t)
	m.setKittyTransport(padui.KittyTransportSharedMemory)
	m.Init()
	m.pic.SetSize(4, 3)
	m.pic.Toggle()
	encoded := encodeFrame(t, m)
	frame := encoded.msg.(picture.KittyFrameMsg)
	if frame.Medium != picture.KittyMediumSharedMemory {
		t.Fatalf("frame used %v, not shared memory", frame.Medium)
	}
	path := sharedObjectPath(t, frame.APC)
	m.Update(encoded) // submitted to a terminal that never reads it
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("submitted frame's object is already gone: %v", err)
	}
	m.close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("object %s survived close (stat error: %v)", path, err)
	}
}
