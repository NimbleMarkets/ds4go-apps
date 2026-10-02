package main

import (
	"image"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui/padtest"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

func kittyTransportModel(t *testing.T, transport padui.KittyTransport) *model {
	t.Helper()
	previous := picture.KittySupported()
	picture.ForceKittyCapability(picture.KittyCapabilitySupported)
	padtest.AllowKittySharedMemory(t)
	t.Cleanup(func() { picture.ForceKittyCapability(previous) })
	m := testModel(t)
	m.setKittyTransport(transport)
	m.Init()
	m.pic.SetSize(4, 3)
	m.pic.Toggle()
	return m
}

// encodeFrame runs one frame through the model up to the encoded message.
func encodeFrame(t *testing.T, m *model) encodedMsg {
	t.Helper()
	m.rendering = true
	_, encode := m.Update(frameMsg{img: image.NewNRGBA(image.Rect(0, 0, 32, 48))})
	if encode == nil {
		t.Fatal("missing Kitty encoding command")
	}
	encoded, ok := encode().(encodedMsg)
	if !ok {
		t.Fatal("expected encoded frame")
	}
	return encoded
}

func TestKittyTransportReachesTelemetry(t *testing.T) {
	shared := "shm"
	if !picture.KittySharedMemorySupported() {
		shared = "png" // direct fallback
	}
	for _, tc := range []struct {
		request padui.KittyTransport
		want    string
	}{
		{padui.KittyTransportPNG, "png"},
		{padui.KittyTransportRGBA, "rgba"},
		{padui.KittyTransportSharedMemory, shared},
	} {
		m := kittyTransportModel(t, tc.request)
		encoded := encodeFrame(t, m)
		m.Update(encoded)
		perf := m.state.Snapshot().Performance
		if perf.Transport != tc.want {
			t.Fatalf("requested %q: telemetry reports %q, want %q", tc.request, perf.Transport, tc.want)
		}
		// 32x48 raw RGBA is 6144 bytes. Direct RGBA must show that cost in the
		// terminal stream; shared memory must not.
		if tc.want == "rgba" && perf.UploadBytes < 6144 {
			t.Fatalf("direct RGBA reported only %d upload bytes", perf.UploadBytes)
		}
		if tc.want == "shm" && perf.UploadBytes >= 512 {
			t.Fatalf("shared memory still wrote %d bytes to the terminal", perf.UploadBytes)
		}
	}
}

func countLog(m *model, needle string) int {
	n := 0
	for _, line := range m.log {
		if strings.Contains(line, needle) {
			n++
		}
	}
	return n
}

// Shared memory falls back silently inside the widget. The user asked for it,
// so say once that it is not in effect, and do not repeat it every frame.
func TestSharedMemoryFallbackIsLoggedOnce(t *testing.T) {
	m := kittyTransportModel(t, padui.KittyTransportSharedMemory)
	fallback := encodedMsg{msg: picture.KittyFrameMsg{}, performance: tools.Performance{Kitty: true, Transport: "png"}}
	m.Update(fallback)
	m.Update(fallback)
	if got := countLog(m, "shared memory"); got != 1 {
		t.Fatalf("fallback logged %d times, want 1:\n%s", got, strings.Join(m.log, "\n"))
	}
}

func TestMatchingTransportIsNotLoggedAsFallback(t *testing.T) {
	for _, tc := range []struct {
		request padui.KittyTransport
		actual  string
	}{
		{padui.KittyTransportSharedMemory, "shm"},
		{padui.KittyTransportPNG, "png"},
		{padui.KittyTransportRGBA, "rgba"},
	} {
		m := kittyTransportModel(t, tc.request)
		m.Update(encodedMsg{msg: picture.KittyFrameMsg{}, performance: tools.Performance{Kitty: true, Transport: tc.actual}})
		if got := countLog(m, "shared memory"); got != 0 {
			t.Fatalf("%q reported a fallback that did not happen:\n%s", tc.request, strings.Join(m.log, "\n"))
		}
	}
}
