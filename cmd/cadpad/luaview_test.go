package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	m.width, m.height = 90, 30

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

	out := m.sourceOverlay()
	if !strings.Contains(out, "cadpad.tower.lua") {
		t.Error("overlay missing script name")
	}
	if !strings.Contains(ansi.Strip(out), "sdf.register") {
		t.Error("overlay missing source content")
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

func TestSourceScrollClamps(t *testing.T) {
	m := luaViewTestModel(t)
	m.toggleSourceView()

	if top := m.sourceScrollBy(-5); top != 0 {
		t.Errorf("scroll above top = %d, want 0", top)
	}
	m.sourceTop = 0
	if top := m.sourceScrollBy(1000); top > len(m.sourceLines) {
		t.Errorf("scroll below bottom = %d, want clamped", top)
	}
}
