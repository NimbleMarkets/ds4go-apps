package main

import (
	"image"
	"strings"
	"testing"
	"time"

	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

func TestKittyAnimationRetainsImageBetweenFrames(t *testing.T) {
	previous := picture.KittySupported()
	picture.ForceKittyCapability(picture.KittyCapabilitySupported)
	t.Cleanup(func() { picture.ForceKittyCapability(previous) })
	m := testModel(t)
	m.Init()
	m.pic.SetSize(4, 3)
	m.pic.SetKittyResolutionFactor(.5)
	m.pic.Toggle()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 24))
	var imageID int
	for i, duration := range []time.Duration{0, 100 * time.Millisecond, 0} {
		m.rendering = true
		_, encode := m.Update(frameMsg{img: img, duration: duration})
		if encode == nil {
			t.Fatal("missing Kitty encoding command")
		}
		encoded, ok := encode().(encodedMsg)
		if !ok {
			t.Fatal("expected encoded frame")
		}
		frame, ok := encoded.msg.(picture.KittyFrameMsg)
		if !ok {
			t.Fatal("expected Kitty frame")
		}
		if strings.Contains(frame.APC, "\x1b_Ga=d") {
			t.Fatalf("frame %d deletes the displayed image before replacement", i)
		}
		if i > 0 && frame.ID != imageID {
			t.Fatal("image ID changed between frames")
		}
		imageID = frame.ID
		_, present := m.Update(encoded)
		perf := m.state.Snapshot().Performance
		if !perf.Kitty || perf.UploadBytes != len(frame.APC) || perf.RenderMS != float64(duration)/float64(time.Millisecond) || perf.EncodeMS < 0 || perf.SampledAt == 0 {
			t.Fatalf("incorrect native telemetry: %+v", perf)
		}
		if present == nil || !m.rendering {
			t.Fatal("frame slot released before terminal presentation")
		}
		m.dirty = true
		if m.renderCmd() != nil {
			t.Fatal("next frame overtook terminal presentation")
		}
		m.Update(presentedMsg{})
		if m.rendering {
			t.Fatal("frame slot not released after presentation")
		}
	}
	if m.downscale != 3 {
		t.Fatal("slow-frame GPU downscaling stopped working")
	}
	// A real placement resize still needs the widget's delete/recreate sequence.
	resized := m.pic.SetSize(5, 3)().(picture.KittyFrameMsg)
	if !strings.HasPrefix(resized.APC, "\x1b_Ga=d") {
		t.Fatal("real resize no longer resets the Kitty placement")
	}
}

func TestFullscreenInvalidatesPendingKittyEncoding(t *testing.T) {
	previous := picture.KittySupported()
	picture.ForceKittyCapability(picture.KittyCapabilitySupported)
	t.Cleanup(func() { picture.ForceKittyCapability(previous) })
	m := testModel(t)
	m.width, m.height = 80, 24
	m.Init()
	cols, rows := m.viewport()
	m.pic.SetSize(cols, rows)
	m.pic.Toggle()
	frame := m.renderCmd()().(frameMsg)
	_, encode := m.Update(frame)
	encoded := encode().(encodedMsg)
	m.toggleFullscreen()
	if m.pic.Update(encoded.msg) != nil {
		t.Fatal("old workspace encoding could overwrite the fullscreen placement")
	}
	m.Update(presentedMsg{})
	fresh := m.renderCmd()().(frameMsg)
	_, encode = m.Update(fresh)
	encoded = encode().(encodedMsg)
	if m.pic.Update(encoded.msg) == nil {
		t.Fatal("fresh fullscreen encoding rejected")
	}
}
