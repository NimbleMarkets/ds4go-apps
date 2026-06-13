package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/charmbracelet/x/ansi"
)

const sampleLua = `-- build a tower
local function tower(h)
  local s = sdf.box(1, 1, h)
  return s:translate(0, 0, h / 2)
end
sdf.register("tower", tower(4))
`

func TestHighlightLuaAddsANSIAndPreservesText(t *testing.T) {
	out := highlightLua(sampleLua)
	if !strings.Contains(out, "\x1b[") {
		t.Error("highlighted source contains no ANSI sequences")
	}
	if got := ansi.Strip(out); got != sampleLua {
		t.Errorf("highlighting altered the text:\n got: %q\nwant: %q", got, sampleLua)
	}
}

func luaViewTestModel(t *testing.T) model {
	t.Helper()
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	m.width, m.height = 100, 36

	path := filepath.Join(t.TempDir(), "cadpad.tower.lua")
	if err := os.WriteFile(path, []byte(sampleLua), 0o644); err != nil {
		t.Fatal(err)
	}
	m.luaEntries = []luaEntry{{filename: "cadpad.tower.lua", path: path}}
	m.luaEntryIndex = 0
	// newModel defaults the panel on and may have loaded a real workspace
	// script; reset so each test drives the fixture explicitly.
	m.showSource = false
	m.sourceName = ""
	m.sourceLines = nil
	return m
}

func TestToggleSourceViewLoadsActiveScript(t *testing.T) {
	m := luaViewTestModel(t)

	m.toggleSourceView()
	if !m.showSource {
		t.Fatal("showSource = false after toggle")
	}
	if m.sourceName != "cadpad.tower.lua" {
		t.Errorf("sourceName = %q", m.sourceName)
	}

	panel := m.sourcePanel(40, 20)
	if !strings.Contains(panel, "cadpad.tower.lua") {
		t.Error("panel missing script name")
	}
	if !strings.Contains(ansi.Strip(panel), "sdf.register") {
		t.Error("panel missing source content")
	}

	m.toggleSourceView()
	if m.showSource {
		t.Error("showSource = true after second toggle")
	}
}

func TestToggleSourceViewWithoutScript(t *testing.T) {
	m := luaViewTestModel(t)
	m.showSource = false
	m.luaEntries = nil
	m.lastActiveLua = ""
	m.sourceLines = nil
	m.sourceName = ""

	// The panel is a fixture of the layout now: it opens even with no
	// script and shows a placeholder until the model writes one.
	m.toggleSourceView()
	if !m.showSource {
		t.Error("showSource = false after toggle, want placeholder panel")
	}
	if panel := ansi.Strip(m.sourcePanel(40, 12)); !strings.Contains(panel, "no lua yet") {
		t.Errorf("panel missing placeholder: %q", panel)
	}
}

// The source panel lives in the body row, to the left of the CAD viewport,
// at full body height; the bottom row and its height accounting are
// untouched by it.
func TestSourceBesideViewport(t *testing.T) {
	m := luaViewTestModel(t)
	m.showThinking = true
	m.lastThinking = "planning the tower"
	m.toggleSourceView()

	body := ansi.Strip(m.bodyView())
	if !strings.Contains(body, "cadpad.tower.lua") || !strings.Contains(body, "view") {
		t.Fatalf("expected source panel and viewport in body row:\n%s", body)
	}
	if bottom := ansi.Strip(m.bottomBoxesView()); strings.Contains(bottom, "cadpad.tower.lua") {
		t.Error("source panel leaked into the bottom boxes")
	}

	withSource := m.bottomBoxesHeight()
	m.showSource = false
	if withoutSource := m.bottomBoxesHeight(); withSource != withoutSource {
		t.Errorf("bottomBoxesHeight with source = %d, without = %d — body panel must not affect it", withSource, withoutSource)
	}

	// The viewport render budget shrinks to make room for the panel.
	m.showSource = true
	colsWith, _ := m.viewportInnerSize()
	m.showSource = false
	colsWithout, _ := m.viewportInnerSize()
	if colsWith >= colsWithout {
		t.Errorf("viewport cols with source = %d, without = %d — want narrower", colsWith, colsWithout)
	}
}

// During generation the panel follows the file the model is writing:
// lastActiveLua wins over the browsed history entry, the panel auto-opens,
// and each tool result reloads the content.
func TestSourceFollowsWritesDuringGeneration(t *testing.T) {
	m := luaViewTestModel(t)
	m.inferencing = true
	active := filepath.Join(t.TempDir(), "current.lua")
	if err := os.WriteFile(active, []byte("local a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The driver log line that records the file being written auto-opens the panel.
	m = updateDriverEvent(t, m, bubble.LogEvent{Level: "info", Message: `tool: lua_write args={"path": "` + active + `"}`})
	if !m.showSource {
		t.Fatal("source panel did not auto-open when generation started writing lua")
	}
	if m.sourceName != "current.lua" {
		t.Errorf("sourceName = %q, want the actively-written file", m.sourceName)
	}

	// New content lands on each tool result.
	if err := os.WriteFile(active, []byte("local a = 1\nsdf.register(\"a\", sdf.sphere(a))\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = updateDriverEvent(t, m, bubble.ToolResultsEvent{})
	joined := ansi.Strip(strings.Join(m.sourceLines, "\n"))
	if !strings.Contains(joined, "sdf.register") {
		t.Errorf("source not reloaded after tool result: %q", joined)
	}
}

func updateDriverEvent(t *testing.T, m model, e bubble.Event) model {
	t.Helper()
	next, _ := m.Update(driverEventMsg{e: e})
	nm, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return nm
}

func TestSourceScrollClampsAndFollowsTail(t *testing.T) {
	m := luaViewTestModel(t)
	m.toggleSourceView()

	if m.sourceScroll != 0 {
		t.Errorf("sourceScroll = %d on open, want 0 (follow tail)", m.sourceScroll)
	}
	m.sourceScroll = 10000
	panel := m.sourcePanel(60, 8)
	if !strings.Contains(ansi.Strip(panel), "-- build a tower") {
		t.Error("over-scrolled panel should clamp to the top of the file")
	}
}

func TestSourceShownByDefault(t *testing.T) {
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	if !m.showSource {
		t.Error("showSource = false on startup, want the lua panel visible by default")
	}
}

// The body row is flexbox-managed: objects + metrics bubbles stacked on the
// left, lua source beside them, viewport taking the rest. Mouse hit-testing
// must account for the source column.
func TestBodyBubblesAndViewportBounds(t *testing.T) {
	m := luaViewTestModel(t)
	m.showSource = true
	m.reloadSource()
	m.w.Create("box1", "box", map[string]float64{"x": 4, "y": 3, "z": 2})

	body := ansi.Strip(m.bodyView())
	for _, want := range []string{"Objects", "Metrics", "cadpad.tower.lua", "box1"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}

	x0With, _, _, _ := m.viewportBounds()
	m.showSource = false
	x0Without, _, _, _ := m.viewportBounds()
	if x0With <= x0Without {
		t.Errorf("viewport x0 with source = %d, without = %d — bounds must shift right of the source column", x0With, x0Without)
	}
}

// On startup the viewport must show the same script the source panel
// displays: Init executes the active lua entry into the world (the
// debug_box seed is only a fallback for an empty workspace).
func TestInitSyncsWorldWithSourcePanel(t *testing.T) {
	m := luaViewTestModel(t)
	m.showSource = true
	m.reloadSource()

	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init returned nil batch")
	}
	if !m.w.Has("tower") {
		t.Errorf("world missing %q from the startup script; objects = %v", "tower", m.w.Names())
	}
	if m.w.Has("debug_box") {
		t.Error("debug_box seeded even though a workspace script exists")
	}
}

func TestInitSeedsDebugBoxWithoutScripts(t *testing.T) {
	m := luaViewTestModel(t)
	m.luaEntries = nil

	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init returned nil batch")
	}
	if !m.w.Has("debug_box") {
		t.Errorf("expected debug_box fallback, objects = %v", m.w.Names())
	}
}

// Init runs before the terminal size is known, so the initial preview must
// wait for the first WindowSizeMsg — rendering at width 0 produces a tiny
// raster that the resize handler (which deliberately never re-renders)
// would leave on screen.
func TestInitialPreviewRendersAtFirstWindowSize(t *testing.T) {
	m := luaViewTestModel(t)
	m.width, m.height = 0, 0
	m.w.Create("box1", "box", map[string]float64{"x": 1, "y": 1, "z": 1})

	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = m2.(model)
	if !m.renderingPreview {
		t.Error("first WindowSizeMsg did not kick off the initial preview render")
	}

	// Subsequent resizes keep the no-render-storm behavior.
	m.renderingPreview = false
	m2, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: 38})
	m = m2.(model)
	if m.renderingPreview {
		t.Error("later resizes must not auto-render")
	}
}

// An intermediate successful lua_run during a generation must show up in
// the viewport immediately, not at end of turn.
func TestIntermediateLuaRunRefreshesPreview(t *testing.T) {
	m := luaViewTestModel(t)
	m.inferencing = true
	m.w.Create("ball", "sphere", map[string]float64{"r": 2})
	m.renderingPreview = false

	m = updateDriverEvent(t, m, bubble.ToolResultsEvent{Results: []ds4.ChatMessage{
		{Role: "tool", Content: "Executed current.lua successfully. World updated."},
	}})
	if !m.renderingPreview {
		t.Error("successful intermediate lua_run did not refresh the preview")
	}

	// A failed run must not waste a render on unchanged geometry.
	m.renderingPreview = false
	m = updateDriverEvent(t, m, bubble.ToolResultsEvent{Results: []ds4.ChatMessage{
		{Role: "tool", Content: "Lua execution error in current.lua:\nsyntax error"},
	}})
	if m.renderingPreview {
		t.Error("failed lua_run should not refresh the preview")
	}
}

func TestToolLoopBudget(t *testing.T) {
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	if m.maxRounds != 36 {
		t.Errorf("maxRounds = %d, want 36", m.maxRounds)
	}
}

// topBorderColor returns the first SGR escape sequence in a rendered box,
// i.e. its border color.
func topBorderColor(rendered string) string {
	if i := strings.Index(rendered, "\x1b["); i >= 0 {
		if j := strings.IndexByte(rendered[i:], 'm'); j >= 0 {
			return rendered[i : i+j+1]
		}
	}
	return ""
}

// tab must cycle focus onto the source panel, and the panel must use the
// same neutral border as the other panels when unfocused and a distinct
// bright border when focused — yellow-on-yellow gave no visible cue.
func TestSourcePanelFocusIsVisible(t *testing.T) {
	m := luaViewTestModel(t)
	m.showSource = true
	m.reloadSource()
	m.w.Create("ball", "sphere", map[string]float64{"r": 2})

	unfocused := m.sourcePanel(40, 12)
	neutral := topBorderColor(m.objectsBubble(20, 10)) // always-unfocused reference
	if got := topBorderColor(unfocused); got != neutral {
		t.Errorf("unfocused source border = %q, want neutral %q (match other panels)", got, neutral)
	}

	m2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = m2.(model)
	if m.focus != focusSource {
		t.Fatalf("focus = %v after tab, want focusSource", m.focus)
	}
	focused := m.sourcePanel(40, 12)
	if topBorderColor(focused) == neutral {
		t.Error("focused source border is still neutral — no visible focus indicator")
	}
}
