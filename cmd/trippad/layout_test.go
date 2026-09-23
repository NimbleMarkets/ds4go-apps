package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/charmbracelet/x/ansi"
)

func TestWorkspaceFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {100, 32}, {160, 58}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := testModel(t)
			m.width, m.height = size[0], size[1]
			m.input.SetValue(strings.Repeat("🌈 shader ", 30))
			m.input.SetWidth(m.width - 4)
			m.input.Focus()
			m.activity = []activityRound{{thinking: strings.Repeat("光 shader ", 200), reply: "Latest model reply", tools: []string{"trip_validate_shader"}}}
			m.log = []string{strings.Repeat("long diagnostic ", 200)}
			view := m.View().Content
			if lipgloss.Width(view) != m.width || lipgloss.Height(view) != m.height {
				t.Fatalf("view %dx%d exceeds terminal %v", lipgloss.Width(view), lipgloss.Height(view), size)
			}
			for i, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) != m.width {
					t.Errorf("row %d width %d, want %d", i, lipgloss.Width(line), m.width)
				}
			}
			for _, title := range []string{"ANIMATION", "PARAMETERS", "THINKING / OUTPUT", "TOOLS / LOG", "PROMPT"} {
				if !strings.Contains(view, title) {
					t.Errorf("missing %s", title)
				}
			}
			cols, rows := m.viewport()
			if cols+2+m.layout().controlsW != m.width || rows+2 != m.layout().mainH {
				t.Fatal("renderer dimensions include panel chrome")
			}
		})
	}
}

func TestFullscreenPreservesDraftAndGeneration(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	m.playing = false
	m.input.SetValue("keep this draft")
	m.input.Focus()
	m.gen, _ = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		<-ctx.Done()
	})
	before := m.state.Snapshot()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !m.fullscreen || cmd == nil {
		t.Fatal("Ctrl+F did not resize paused viewport")
	}
	if w, h := m.viewport(); w != 80 || h != 24 {
		t.Fatalf("fullscreen viewport %dx%d", w, h)
	}
	view := ansi.Strip(m.View().Content)
	if strings.TrimSpace(view) != "" || lipgloss.Width(view) != 80 || lipgloss.Height(view) != 24 {
		t.Fatal("fullscreen contains UI chrome or fails to fill the screen")
	}
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.fullscreen || m.gen.Canceled() || !m.input.Focused() || m.input.Value() != "keep this draft" || m.playing {
		t.Fatal("leaving fullscreen changed generation, prompt, focus or playback")
	}
	if m.state.Snapshot().Revision != before.Revision {
		t.Fatal("mode switch edited the shader")
	}
	m.key(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if m.fullscreen || m.input.Value() != "keep this draftf" {
		t.Fatal("f shortcut intercepted prompt text")
	}
}

func TestFullscreenDropsOldGeometryAndResizesWhilePaused(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	m.playing = false
	old := m.renderCmd()().(frameMsg)
	m.toggleFullscreen()
	m.Update(old)
	if m.frame != 0 || m.rendering || !m.dirty {
		t.Fatal("accepted in-flight frame from old geometry")
	}
	frame := m.renderCmd()().(frameMsg)
	if frame.cols != 80 || frame.rows != 24 {
		t.Fatal("fullscreen render did not use full terminal")
	}
	m.Update(frame)
	if strings.TrimSpace(ansi.Strip(m.View().Content)) == "" || strings.Contains(m.View().Content, "PARAMETERS") {
		t.Fatal("fullscreen did not present the animation alone")
	}
	if m.frame != 1 {
		t.Fatal("fresh frame was dropped")
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if w, h := m.viewport(); w != 100 || h != 30 || !m.rendering {
		t.Fatal("paused fullscreen did not redraw on resize")
	}
	// Fullscreen remains usable below the workspace's minimum terminal size.
	m.width, m.height = 20, 8
	if strings.Contains(m.View().Content, "enlarge") {
		t.Fatal("small fullscreen blocked")
	}
}
