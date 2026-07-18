package main

import (
	"image"
	"io"
	"log"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
)

// picViewRows returns the number of text rows the picture widget currently
// renders. In the test environment the widget is in Glyph mode, whose View
// emits exactly one line per cell row, so the line count equals the widget's
// row budget — a proxy for "was the picture told its new size?".
func picViewRows(m model) int {
	c := m.pic.View().Content
	if c == "" {
		return 0
	}
	return len(strings.Split(c, "\n"))
}

// sizedViewportModel returns a model sized to 100x50 with a non-nil image in
// the picture widget and both bottom-box toggles off, ready to observe how a
// layout change affects the picture's row budget.
func sizedViewportModel(t *testing.T) model {
	t.Helper()
	m := newModel(testApp(log.New(io.Discard, "", 0), ds4log.NewBuffer(10)))
	m.showThinking = false
	m.showLuaOutput = false
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	m = m2.(model)
	if cmd := m.pic.SetImage(image.NewRGBA(image.Rect(0, 0, 120, 90))); cmd != nil {
		next, _ := m.Update(cmd())
		m = next.(model)
	}
	return m
}

// TestLayoutTogglesResizeViewport is a regression test for the 3D viewport not
// filling its panel until a manual terminal resize. The bottom-box toggles
// (LLM-output "T" and Lua-output "L") change bodyH() and therefore the
// viewport's row budget; like the source-panel toggle they must call
// pic.SetSize so the picture refits immediately instead of staying at the
// stale size until the next WindowSizeMsg.
func TestLayoutTogglesResizeViewport(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyPressMsg
		flag func(model) bool
	}{
		{"thinking", tea.KeyPressMsg{Code: 'T', Text: "T"}, func(m model) bool { return m.showThinking }},
		{"luaOutput", tea.KeyPressMsg{Code: 'L', Text: "L"}, func(m model) bool { return m.showLuaOutput }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sizedViewportModel(t)

			_, rowsBefore := m.viewportInnerSize()
			if got := picViewRows(m); got != rowsBefore {
				t.Fatalf("precondition: picture has %d rows, viewport budget is %d", got, rowsBefore)
			}

			m, _ = m.handleKeyMsg(tc.key)
			if !tc.flag(m) {
				t.Fatalf("%s toggle did not flip its flag", tc.name)
			}

			_, rowsAfter := m.viewportInnerSize()
			if rowsAfter == rowsBefore {
				t.Fatalf("%s toggle did not change the viewport row budget (%d); test cannot detect a resize", tc.name, rowsAfter)
			}
			if got := picViewRows(m); got != rowsAfter {
				t.Fatalf("picture not resized after %s toggle: has %d rows, viewport budget is %d (must call pic.SetSize)", tc.name, got, rowsAfter)
			}
		})
	}
}
