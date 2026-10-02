package main

import (
	"image"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui/padtest"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

func kittyTransportModel(t *testing.T, transport padui.KittyTransport) model {
	t.Helper()
	previous := picture.KittySupported()
	picture.ForceKittyCapability(picture.KittyCapabilitySupported)
	padtest.AllowKittySharedMemory(t)
	t.Cleanup(func() { picture.ForceKittyCapability(previous) })
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	m.setKittyTransport(transport)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(model)
	m.proj = render.ProjAngle
	if m.pic.Mode() != picture.PictureKitty {
		m.pic.Toggle()
	}
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	next, _ = m.Update(previewUpdatedMsg{name: "x", ok: true, img: img, mode: render.RenderModeGPU, hasMode: true, render: time.Millisecond})
	return next.(model)
}

func TestPreviewUsesRequestedKittyTransport(t *testing.T) {
	shared := padui.KittyTransportSharedMemory
	if !picture.KittySharedMemorySupported() {
		shared = padui.KittyTransportPNG // direct fallback
	}
	for _, tc := range []struct{ request, want padui.KittyTransport }{
		{padui.KittyTransportPNG, padui.KittyTransportPNG},
		{padui.KittyTransportRGBA, padui.KittyTransportRGBA},
		{padui.KittyTransportSharedMemory, shared},
		{padui.KittyTransportAuto, shared},
	} {
		m := kittyTransportModel(t, tc.request)
		cmd := m.presentPreview(image.NewRGBA(image.Rect(0, 0, 640, 480)))
		if cmd == nil {
			t.Fatal("no encode command in Kitty mode")
		}
		msg := cmd().(padui.FrameEncodedMsg)
		m.shutdown() // releases any shared-memory object
		if msg.Transport != tc.want {
			t.Fatalf("requested %q: frame used %q, want %q", tc.request, msg.Transport, tc.want)
		}
	}
}

// The header's frame cost is only comparable between runs if it says how the
// frame travelled. Plain PNG stays unlabelled to keep the default header short.
func TestViewportHeaderNamesTransport(t *testing.T) {
	for _, tc := range []struct {
		request, actual padui.KittyTransport
		want            string // "" means no transport label
	}{
		{padui.KittyTransportPNG, padui.KittyTransportPNG, ""},
		{padui.KittyTransportRGBA, padui.KittyTransportRGBA, "rgba"},
		{padui.KittyTransportSharedMemory, padui.KittyTransportSharedMemory, "shm"},
		// Requested shared memory but the widget fell back: say so.
		{padui.KittyTransportSharedMemory, padui.KittyTransportPNG, "png"},
		// Auto landing on PNG is the ordinary case, not worth a label; once
		// shared memory is in use the label shows it.
		{padui.KittyTransportAuto, padui.KittyTransportPNG, ""},
		{padui.KittyTransportAuto, padui.KittyTransportSharedMemory, "shm"},
	} {
		m := kittyTransportModel(t, tc.request)
		next, _ := m.Update(padui.FrameEncodedMsg{Msg: picture.KittyFrameMsg{}, Encode: 3 * time.Millisecond, Bytes: 100, Kitty: true, Transport: tc.actual})
		m = next.(model)
		out := m.viewportView(120)
		for _, label := range []string{"png", "rgba", "shm"} {
			if has := strings.Contains(out, "ms "+label); has != (label == tc.want) {
				t.Fatalf("request=%q actual=%q: label %q present=%v, want label %q", tc.request, tc.actual, label, has, tc.want)
			}
		}
	}
}
