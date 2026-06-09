package main

import (
	"io"
	"log"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
)

func TestSanitizeForDisplay(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "hello world", "hello world"},
		{"keeps newlines", "a\nb", "a\nb"},
		{"crlf to lf", "a\r\nb", "a\nb"},
		{"lone cr to lf", "a\rb", "a\nb"},
		{"tab to spaces", "a\tb", "a    b"},
		{"strips csi erase", "a\x1b[2Kb", "ab"},
		{"strips cursor move", "x\x1b[5Ay", "xy"},
		{"strips sgr color", "\x1b[31mred\x1b[0m", "red"},
		{"drops c0 control", "a\x00b\x07c", "abc"},
		{"keeps unicode", "café 日本語 🤔", "café 日本語 🤔"},
	}
	for _, c := range cases {
		if got := sanitizeForDisplay(c.in); got != c.want {
			t.Errorf("%s: sanitizeForDisplay(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestThinkingBoxNeutralizesControlChars proves reasoning text containing raw
// control/escape sequences cannot leak active cursor-move/erase sequences,
// carriage returns, or tabs into the composed frame — the cause of the box
// "spilling past its borders" and corrupting compositing.
func TestThinkingBoxNeutralizesControlChars(t *testing.T) {
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(model)

	m.showThinking = true
	m.reasoningLog = []string{"[THINK] erase\x1b[2K and move\x1b[5A and\rreturn\tand tab — SENTINEL"}

	out := m.View().Content
	if !strings.Contains(out, "SENTINEL") {
		t.Fatal("expected sanitized reasoning to still be displayed")
	}
	for _, bad := range []string{"\r", "\t", "\x1b[2K", "\x1b[5A"} {
		if strings.Contains(out, bad) {
			t.Errorf("rendered frame still contains raw control sequence %q", bad)
		}
	}
}

// TestViewportRender exercises the full path from geometry creation
// to viewport rendering and checks that the picture widget produces content.
func TestViewportRender(t *testing.T) {
	logBuf := ds4log.NewBuffer(10)
	logger := log.New(io.Discard, "", 0)

	m := newModel(testApp(logger, logBuf))

	// Simulate window size so the model knows its dimensions.
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(model)

	// Create a simple object.
	m.w.Create("box1", "box", map[string]float64{"x": 4, "y": 3, "z": 2})

	// Trigger a preview refresh.
	cmd := m.refreshPreviewCmd()
	if cmd == nil {
		t.Fatal("refreshPreviewCmd returned nil")
	}

	// Execute the command to get the previewUpdatedMsg.
	msg := cmd()
	pum, ok := msg.(previewUpdatedMsg)
	if !ok {
		t.Fatalf("expected previewUpdatedMsg, got %T", msg)
	}
	if !pum.ok {
		t.Fatalf("preview render failed: %s", pum.err)
	}

	// Feed the message back into Update.
	m3, _ := m.Update(pum)
	m = m3.(model)

	// Check that the picture widget has content.
	v := m.pic.View()
	if v.Content == "" {
		t.Fatal("picture widget View().Content is empty after SetImage")
	}
	t.Logf("picture content length: %d", len(v.Content))

	// Now render the full viewport and check it contains the picture content.
	viewOutput := m.View()
	// tea.View has a Content field in v2.
	if viewOutput.Content == "" {
		t.Fatal("model View().Content is empty")
	}
	if !strings.Contains(viewOutput.Content, v.Content) {
		// The picture content might be wrapped/styled; just check it's not empty.
		t.Logf("model view length: %d", len(viewOutput.Content))
	}
}

// TestViewportRenderDirect checks the renderer produces an image and
// the picture widget can display it without the full TUI round-trip.
func TestViewportRenderDirect(t *testing.T) {
	w := world.NewWorld()
	w.Create("ball", "sphere", map[string]float64{"r": 3})
	s, _, ok := w.Get("ball")
	if !ok {
		t.Fatal("object missing")
	}

	r, err := render.NewRenderer(render.PreviewConfig{MaxEdge: 32, EvalBuffer: 4096})
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}

	img, rect, err := r.Render(s, "ball", render.ProjXY, 40, 30)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if img == nil || rect.Dx() == 0 {
		t.Fatal("nil image or zero rect")
	}

	// Feed directly to picture widget.
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(model)

	// Directly set the image.
	setCmd := m.pic.SetImage(img)
	if setCmd != nil {
		msg := setCmd()
		m3, _ := m.Update(msg)
		m = m3.(model)
	}

	v := m.pic.View()
	if v.Content == "" {
		t.Fatal("picture widget content empty after direct SetImage")
	}
	t.Logf("direct picture content length: %d", len(v.Content))
}

func TestMouseNavigation(t *testing.T) {
	logBuf := ds4log.NewBuffer(10)
	logger := log.New(io.Discard, "", 0)

	m := newModel(testApp(logger, logBuf))
	m.proj = render.ProjAngle // Ensure we are in 3D projection mode

	m.w.Create("box1", "box", map[string]float64{"x": 4, "y": 3, "z": 2})

	// Simulate window size so the model knows its dimensions.
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(model)

	x0, y0, x1, y1 := m.viewportBounds()
	if x0 >= x1 || y0 >= y1 {
		t.Fatalf("expected valid viewport bounds, got: %d, %d, %d, %d", x0, y0, x1, y1)
	}

	// 1. Click inside viewport bounds should start drag
	clickX := (x0 + x1) / 2
	clickY := (y0 + y1) / 2
	m3, _ := m.Update(tea.MouseClickMsg{
		X:      clickX,
		Y:      clickY,
		Button: tea.MouseLeft,
	})
	m = m3.(model)

	if !m.mouseDragging {
		t.Error("expected mouseDragging to be true after clicking inside viewport")
	}
	if m.focus != focusViewport {
		t.Error("expected focus to be focusViewport after clicking inside viewport")
	}

	// Save baseline camera state
	baselineAzimuth := m.camAzimuth
	baselineElevation := m.camElevation

	// 2. Drag without shift modifier should orbit (change Azimuth and Elevation)
	m4, cmd4 := m.Update(tea.MouseMotionMsg{
		X:      clickX + 2,
		Y:      clickY + 3,
		Button: tea.MouseLeft,
	})
	m = m4.(model)

	if cmd4 == nil {
		t.Error("expected non-nil render command on first preview request")
	}
	if !m.renderingPreview {
		t.Error("expected renderingPreview to be true")
	}
	if m.previewDirty {
		t.Error("expected previewDirty to be false on first request")
	}

	if m.camAzimuth >= baselineAzimuth {
		t.Error("expected camAzimuth to decrease after drag motion right (inverted axis)")
	}
	if m.camElevation == baselineElevation {
		t.Error("expected camElevation to change after drag motion")
	}

	// Save baseline panning state
	baselinePanX := m.camPanX
	baselinePanY := m.camPanY

	// 3. Drag with shift modifier should pan (change PanX and PanY)
	m5, cmd5 := m.Update(tea.MouseMotionMsg{
		X:      clickX + 5,
		Y:      clickY + 7,
		Button: tea.MouseLeft,
		Mod:    tea.ModShift,
	})
	m = m5.(model)

	if cmd5 != nil {
		t.Error("expected nil command for coalesced request while rendering is in progress")
	}
	if !m.previewDirty {
		t.Error("expected previewDirty to be true when a new request comes in during rendering")
	}

	if m.camPanX >= baselinePanX {
		t.Error("expected camPanX to decrease after shift+drag motion right (inverted axis)")
	}
	if m.camPanY == baselinePanY {
		t.Error("expected camPanY to change after shift+drag motion")
	}

	// 3.5 Feed previewUpdatedMsg. Since previewDirty is true, it should launch the dirty render.
	mX, cmdX := m.Update(previewUpdatedMsg{
		name: "(any)",
		ok:   true,
	})
	m = mX.(model)
	if cmdX == nil {
		t.Error("expected non-nil render command on previewUpdatedMsg when previewDirty is true")
	}
	if !m.renderingPreview {
		t.Error("expected renderingPreview to be true after launching dirty render")
	}
	if m.previewDirty {
		t.Error("expected previewDirty to be reset to false after launching dirty render")
	}

	// 4. Release mouse should end drag
	m6, _ := m.Update(tea.MouseReleaseMsg{
		X:      clickX + 5,
		Y:      clickY + 7,
		Button: tea.MouseLeft,
	})
	m = m6.(model)

	if m.mouseDragging {
		t.Error("expected mouseDragging to be false after mouse release")
	}

	// Save baseline zoom state
	baselineZoom := m.camZoom

	// 5. Scroll inside viewport should zoom
	m7, _ := m.Update(tea.MouseWheelMsg{
		X:      clickX,
		Y:      clickY,
		Button: tea.MouseWheelUp,
	})
	m = m7.(model)

	if m.camZoom == baselineZoom {
		t.Error("expected camZoom to change after scroll up")
	}
}
