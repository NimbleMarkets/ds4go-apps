package main

import (
	"image"
	"io"
	"log"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
)

func progressiveTestModel(t *testing.T) model {
	t.Helper()
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	m.proj = render.ProjAngle
	m.w.Create("ball", "sphere", map[string]float64{"r": 3})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	m = m2.(model)
	// The first WindowSizeMsg kicks off the initial preview; model the
	// steady state where that render has completed.
	m.renderingPreview = false
	return m
}

func TestInteractiveRefreshUsesLowResAndArmsIdleTimer(t *testing.T) {
	m := progressiveTestModel(t)

	cmd := m.refreshPreviewInteractive()
	if cmd == nil {
		t.Fatal("expected a render + idle-timer command")
	}
	if !m.previewLowRes {
		t.Error("previewLowRes = false, want true during camera interaction")
	}
	if m.camSeq != 1 {
		t.Errorf("camSeq = %d, want 1", m.camSeq)
	}

	// A full-resolution refresh clears the interactive flag.
	m.renderingPreview = false
	if cmd := m.refreshPreview(); cmd == nil {
		t.Fatal("expected full-res render command")
	}
	if m.previewLowRes {
		t.Error("previewLowRes = true after a full-res refresh, want false")
	}
}

func TestCamIdleTriggersFullResRender(t *testing.T) {
	m := progressiveTestModel(t)
	m.previewLowRes = true
	m.camSeq = 3

	m2, cmd := m.Update(camIdleMsg{seq: 3})
	m = m2.(model)
	if m.previewLowRes {
		t.Error("previewLowRes = true after idle, want false (full-res rerender)")
	}
	if cmd == nil {
		t.Error("expected a full-res render command on idle")
	}
}

func TestStaleCamIdleIsIgnored(t *testing.T) {
	m := progressiveTestModel(t)
	m.previewLowRes = true
	m.camSeq = 4

	m2, cmd := m.Update(camIdleMsg{seq: 3})
	m = m2.(model)
	if !m.previewLowRes {
		t.Error("previewLowRes cleared by a stale idle tick")
	}
	if cmd != nil {
		t.Error("stale idle tick should not trigger a render")
	}
}

// The low-res pass must actually shrink the traced pixel budget — that is
// the entire speedup.
func TestLowResRenderIsSmaller(t *testing.T) {
	m := progressiveTestModel(t)

	renderOnce := func(lowRes bool) image.Rectangle {
		t.Helper()
		m.previewLowRes = lowRes
		msg := m.refreshPreviewCmd()()
		pu, ok := msg.(previewUpdatedMsg)
		if !ok || !pu.ok {
			t.Fatalf("preview failed: %+v", msg)
		}
		return pu.img.Bounds()
	}

	full := renderOnce(false)
	low := renderOnce(true)
	if low.Dx()*2 >= full.Dx() || low.Dy()*2 >= full.Dy() {
		t.Errorf("low-res %v not meaningfully smaller than full %v", low, full)
	}
}
