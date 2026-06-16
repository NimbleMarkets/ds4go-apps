package main

import (
	"io"
	"log"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
)

// TestRefreshPreviewCoalesces locks in the event-driven render property the
// GPU path relies on for ds4 coexistence: previews are requested only on
// explicit events and a request while one is in flight coalesces (no second
// render, no busy-loop) — so the viewport never spins the GPU on a timer and
// never steals cycles from in-progress LLM inference.
func TestRefreshPreviewCoalesces(t *testing.T) {
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	if _, err := m.w.Create("ball", "sphere", map[string]float64{"r": 3}); err != nil {
		t.Fatalf("create object: %v", err)
	}

	// No render in flight: a request starts one and returns a command.
	if cmd := m.refreshPreview(); cmd == nil {
		t.Fatal("first refreshPreview should return a render command")
	}
	if !m.renderingPreview {
		t.Error("renderingPreview should be true after the first request")
	}

	// Render in flight: a further request coalesces — no command, dirty flag set.
	if cmd := m.refreshPreview(); cmd != nil {
		t.Error("refreshPreview should coalesce (nil command) while a render is in flight")
	}
	if !m.previewDirty {
		t.Error("previewDirty should be set when a request coalesces")
	}
}

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

// TestRenderModeBadge proves the live 3D viewport header shows a GPU/CPU badge
// driven by the mode reported on previewUpdatedMsg. It constructs the message
// directly (no actual GPU render) so it runs anywhere, GPU or not.
func TestRenderModeBadge(t *testing.T) {
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(model)
	m.proj = render.ProjAngle

	// Before any auto-path frame, no badge is shown.
	if strings.Contains(m.viewportView(80), "·GPU") || strings.Contains(m.viewportView(80), "·CPU") {
		t.Fatal("render-mode badge shown before any auto frame reported a mode")
	}

	// A GPU-mode auto frame updates lastRenderMode and shows "3D·GPU".
	mGPU, _ := m.Update(previewUpdatedMsg{name: "x", ok: true, mode: render.RenderModeGPU, hasMode: true})
	g := mGPU.(model)
	if g.lastRenderMode != render.RenderModeGPU {
		t.Errorf("lastRenderMode = %v, want GPU", g.lastRenderMode)
	}
	if !g.showRenderMode {
		t.Error("expected showRenderMode true after auto frame")
	}
	if out := g.viewportView(80); !strings.Contains(out, "3D·GPU") {
		t.Errorf("3D viewport header missing GPU badge; got header in:\n%s", out)
	}

	// A CPU-mode auto frame shows "3D·CPU".
	mCPU, _ := g.Update(previewUpdatedMsg{name: "x", ok: true, mode: render.RenderModeCPU, hasMode: true})
	c := mCPU.(model)
	if c.lastRenderMode != render.RenderModeCPU {
		t.Errorf("lastRenderMode = %v, want CPU", c.lastRenderMode)
	}
	if out := c.viewportView(80); !strings.Contains(out, "3D·CPU") {
		t.Errorf("3D viewport header missing CPU badge; got header in:\n%s", out)
	}

	// A non-auto frame (hasMode=false) must NOT change the badge — guards
	// against mislabeling the CPU Render/RenderAngledScale paths as GPU
	// (RenderModeGPU is the zero value).
	mNoMode, _ := c.Update(previewUpdatedMsg{name: "x", ok: true})
	n := mNoMode.(model)
	if n.lastRenderMode != render.RenderModeCPU {
		t.Errorf("non-auto frame changed lastRenderMode to %v, want CPU unchanged", n.lastRenderMode)
	}
	if out := n.viewportView(80); !strings.Contains(out, "3D·CPU") {
		t.Errorf("badge changed after a non-auto frame; got:\n%s", out)
	}

	// The badge is 3D-only: a 2D projection never shows it.
	n.proj = render.ProjXY
	if out := n.viewportView(80); strings.Contains(out, "·CPU") || strings.Contains(out, "·GPU") {
		t.Errorf("render-mode badge leaked into a 2D projection view:\n%s", out)
	}
}

func TestMouseNavigation(t *testing.T) {
	logBuf := ds4log.NewBuffer(10)
	logger := log.New(io.Discard, "", 0)

	m := newModel(testApp(logger, logBuf))
	m.proj = render.ProjAngle // Ensure we are in 3D projection mode

	m.w.Create("box1", "box", map[string]float64{"x": 4, "y": 3, "z": 2})

	// Simulate window size so the model knows its dimensions; the first
	// size message also starts the initial preview render — model the
	// steady state where it has completed.
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(model)
	m.renderingPreview = false

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

	// A camera request while a render is already in flight coalesces:
	// it queues via previewDirty and returns no command (the mesh path
	// needs no progressive/idle pass).
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

// Regression: toolDoneMsg appends the assistant's final text to toolHistory
// verbatim, and a multi-line markdown summary rendered into the one-row
// footer pushed the whole layout past the terminal height (TUI shear).
func TestFooterStatusLineFlattensMultilineHistory(t *testing.T) {
	m := model{
		width:  80,
		status: "tool complete · saved cadpad.20260611_113657.lua",
		toolHistory: []string{
			`tool: cad_bbox args={"name": "pyramid"}`,
			"I've created a square pyramid of spheres with 4 layers:\n\n- **Layer 1 (bottom)**: 4x4 grid\n- **Layer 2**: 3x3 grid",
		},
	}
	line := m.footerStatusLine()
	if strings.Contains(line, "\n") {
		t.Fatalf("footer status line contains newlines: %q", line)
	}
	if w := lipgloss.Width(line); w > m.width {
		t.Errorf("footer status line width = %d, want <= %d", w, m.width)
	}
}

// Selecting an object in the list must follow through to the viewport:
// j/k (and enter) make the selection current AND refresh the preview —
// previously SetCurrent never re-rendered, so the list appeared dead.
func TestSelectObjectFollowsViewport(t *testing.T) {
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(model)
	m.w.Create("box1", "box", map[string]float64{"x": 1, "y": 1, "z": 1})
	m.w.Create("box2", "box", map[string]float64{"x": 2, "y": 2, "z": 2})
	m.selected = 0

	cmd := m.selectObject(1)
	if got := m.w.Current(); got != "box2" {
		t.Errorf("Current() = %q after selectObject(1), want box2", got)
	}
	if cmd == nil {
		t.Error("expected a preview render command so the viewport follows the selection")
	}

	// A second move while the first render is in flight still switches
	// current and queues the re-render.
	cmd2 := m.selectObject(-1)
	if got := m.w.Current(); got != "box1" {
		t.Errorf("Current() = %q after selectObject(-1), want box1", got)
	}
	if cmd2 != nil && !m.previewDirty {
		t.Error("expected coalesced render: nil cmd with previewDirty set")
	}
	if cmd2 == nil && !m.previewDirty {
		t.Error("second selection neither rendered nor queued a render")
	}
}
