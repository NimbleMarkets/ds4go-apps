package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	m.luaEntries = nil
	m.lastActiveLua = ""

	m.toggleSourceView()
	if m.showSource {
		t.Error("showSource = true with no script available")
	}
	if !strings.Contains(m.status, "no lua script") {
		t.Errorf("status = %q, want a no-script notice", m.status)
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
