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
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// The viewport header must say what raster is on screen and what each frame
// cost, so resolution and speed can be judged without instrumentation.
func TestViewportBadgeShowsRasterAndFrameCost(t *testing.T) {
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(model)
	m.proj = render.ProjAngle
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	next, _ := m.Update(previewUpdatedMsg{name: "x", ok: true, img: img, mode: render.RenderModeGPU, hasMode: true, render: 2500 * time.Microsecond})
	m = next.(model)
	out := m.viewportView(120)
	if !strings.Contains(out, "640x480") || !strings.Contains(out, "2.5ms") {
		t.Fatalf("header lacks raster and render time:\n%s", out)
	}
}

func TestPreviewEncodeCostRecorded(t *testing.T) {
	previous := picture.KittySupported()
	picture.ForceKittyCapability(picture.KittyCapabilitySupported)
	t.Cleanup(func() { picture.ForceKittyCapability(previous) })
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(model)
	m.proj = render.ProjAngle
	if m.pic.Mode() != picture.PictureKitty {
		m.pic.Toggle()
	}
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	next, _ := m.Update(previewUpdatedMsg{name: "x", ok: true, img: img, mode: render.RenderModeGPU, hasMode: true, render: time.Millisecond})
	m = next.(model)
	cmd := m.presentPreview(img)
	if cmd == nil {
		t.Fatal("no encode command in Kitty mode")
	}
	msg, ok := cmd().(padui.FrameEncodedMsg)
	if !ok || msg.Bytes == 0 {
		t.Fatalf("expected timed encode, got %T %+v", cmd(), msg)
	}
	next, _ = m.Update(msg)
	m = next.(model)
	if m.lastEncodeBytes != msg.Bytes {
		t.Fatalf("encode bytes not recorded: %d != %d", m.lastEncodeBytes, msg.Bytes)
	}
	if out := m.viewportView(120); !strings.Contains(out, "+") || !strings.Contains(out, "ms") {
		t.Fatalf("header lacks encode cost:\n%s", out)
	}
	if m.pic.View().Content == "" {
		t.Fatal("encoded frame was not forwarded to the picture widget")
	}
}
