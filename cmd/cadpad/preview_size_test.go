package main

import (
	"math"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	uv "github.com/charmbracelet/ultraviolet"
)

// The preview must be rendered for the terminal's real cell pixel size, not an
// assumed 8x16 cell. A 32x34 cell has a very different aspect from 8x16, so a
// render sized from the wrong cell letterboxes inside the viewport.
func TestPreviewRendersAtTerminalCellSize(t *testing.T) {
	m := sizedViewportModel(t)
	next, _ := m.Update(uv.CellSizeEvent{Width: 32, Height: 34})
	m = next.(model)
	if cw, ch := m.pic.CellPixelSize(); cw != 32 || ch != 34 {
		t.Fatalf("cell size event not applied: %dx%d", cw, ch)
	}
	r, err := render.NewRenderer(render.PreviewConfig{MaxEdge: 32, GPUMaxEdge: 64, EvalBuffer: 4096})
	if err != nil {
		t.Fatal(err)
	}
	m.renderer = r
	m.w.Create("ball", "sphere", map[string]float64{"r": 3})

	msg, ok := m.refreshPreviewCmd()().(previewUpdatedMsg)
	if !ok || !msg.ok || msg.img == nil {
		t.Fatalf("preview failed: %+v", msg)
	}
	cols, rows := m.viewportInnerSize()
	want := float64(cols*32) / float64(rows*34)
	b := msg.img.Bounds()
	got := float64(b.Dx()) / float64(b.Dy())
	if math.Abs(got-want)/want > 0.06 {
		t.Fatalf("preview aspect %.3f (%dx%d) does not match viewport %.3f for %dx%d cells of 32x34", got, b.Dx(), b.Dy(), want, cols, rows)
	}
}
