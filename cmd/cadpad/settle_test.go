package main

import (
	"io"
	"log"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
)

// settleTestModel builds a model with one object, sized, in the angled
// projection and reporting GPU as the last render mode — the exact conditions
// under which an interactive frame should schedule a settle tick.
func settleTestModel(t *testing.T) model {
	t.Helper()
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	if _, err := m.w.Create("ball", "sphere", map[string]float64{"r": 3}); err != nil {
		t.Fatalf("create object: %v", err)
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(model)
	m.proj = render.ProjAngle
	m.lastRenderMode = render.RenderModeGPU
	m.renderingPreview = false
	m.previewDirty = false
	return m
}

// TestSettleSchedulesAfterInteractiveGPUFrame: an interactive (hq=false) GPU
// frame in the angled view, with the view stable, schedules a settle tick.
func TestSettleSchedulesAfterInteractiveGPUFrame(t *testing.T) {
	m := settleTestModel(t)

	m2, cmd := m.Update(previewUpdatedMsg{
		name: "ball", ok: true, hasMode: true, mode: render.RenderModeGPU, hq: false,
	})
	m = m2.(model)
	if cmd == nil {
		t.Fatal("expected a settle tick command after an interactive GPU frame, got nil")
	}
	// The interactive frame itself must not start a render.
	if m.renderingPreview {
		t.Error("interactive frame should not set renderingPreview")
	}

	// A CPU frame must NOT schedule a settle (HQ pass is GPU-only).
	mCPU := settleTestModel(t)
	mCPU.lastRenderMode = render.RenderModeCPU
	_, cmdCPU := mCPU.Update(previewUpdatedMsg{
		name: "ball", ok: true, hasMode: true, mode: render.RenderModeCPU, hq: false,
	})
	if cmdCPU != nil {
		t.Error("CPU frame should not schedule a settle tick")
	}

	// An HQ frame must NOT schedule another settle (no render loop).
	mHQ := settleTestModel(t)
	_, cmdHQ := mHQ.Update(previewUpdatedMsg{
		name: "ball", ok: true, hasMode: true, mode: render.RenderModeGPU, hq: true,
	})
	if cmdHQ != nil {
		t.Error("HQ frame should not schedule another settle tick (would loop)")
	}

	// A non-angled projection must NOT schedule a settle.
	m2D := settleTestModel(t)
	m2D.proj = render.ProjXY
	_, cmd2D := m2D.Update(previewUpdatedMsg{
		name: "ball", ok: true, hasMode: true, mode: render.RenderModeGPU, hq: false,
	})
	if cmd2D != nil {
		t.Error("2D projection should not schedule a settle tick")
	}
}

// TestSettleEpochGating: a settle tick triggers an HQ render only when its
// epoch still matches; a stale tick (epoch behind renderEpoch, e.g. the camera
// moved after the tick was scheduled) is ignored.
func TestSettleEpochGating(t *testing.T) {
	m := settleTestModel(t)
	m.renderEpoch = 5

	// Matching epoch, view idle, angled view → HQ render dispatched.
	m2, cmd := m.Update(camSettleMsg{epoch: 5})
	m = m2.(model)
	if cmd == nil {
		t.Fatal("matching-epoch settle should dispatch an HQ render command")
	}
	if !m.renderingPreview {
		t.Error("matching-epoch settle should set renderingPreview")
	}

	// Stale epoch (a newer render was requested in the meantime) → ignored.
	mStale := settleTestModel(t)
	mStale.renderEpoch = 9
	mStale2, cmdStale := mStale.Update(camSettleMsg{epoch: 4})
	mStale = mStale2.(model)
	if cmdStale != nil {
		t.Error("stale-epoch settle must not dispatch an HQ render command")
	}
	if mStale.renderingPreview {
		t.Error("stale-epoch settle must not set renderingPreview")
	}

	// Matching epoch but a render already in flight → ignored (coalescing owns
	// the next frame).
	mBusy := settleTestModel(t)
	mBusy.renderEpoch = 3
	mBusy.renderingPreview = true
	_, cmdBusy := mBusy.Update(camSettleMsg{epoch: 3})
	if cmdBusy != nil {
		t.Error("settle while a render is in flight must not dispatch a second render")
	}
}

// TestRefreshPreviewBumpsEpoch confirms each requested render advances the
// epoch (even when coalescing), which is what invalidates pending settle ticks
// the moment the camera moves again.
func TestRefreshPreviewBumpsEpoch(t *testing.T) {
	m := settleTestModel(t)
	start := m.renderEpoch

	m.renderingPreview = false
	m.refreshPreview()
	if m.renderEpoch != start+1 {
		t.Fatalf("renderEpoch = %d after first refresh, want %d", m.renderEpoch, start+1)
	}

	// Coalesced request (render in flight) still advances the epoch.
	m.refreshPreview()
	if m.renderEpoch != start+2 {
		t.Errorf("renderEpoch = %d after coalesced refresh, want %d", m.renderEpoch, start+2)
	}
}
