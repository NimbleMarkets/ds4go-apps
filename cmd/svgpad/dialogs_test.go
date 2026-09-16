package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
)

func TestSettingsWhileTypingAndRunning(t *testing.T) {
	m := navigationModel()
	m.height = 30
	m.input.SetValue("keep this prompt")
	m.input.SetCursor(4)
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	if !m.showSettings {
		t.Fatal("settings did not open")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.thinkMode != ds4.ThinkHigh {
		t.Fatal("reasoning did not change")
	}
	m.settingsIndex = 2
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.visual.MaxPasses != 4 {
		t.Fatal("review budget did not change")
	}
	m.captureRequestReasoning()
	m.generating = true
	m.settingsIndex = 0
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.thinkMode != ds4.ThinkMax || m.activeReasoning() != ds4.ThinkHigh {
		t.Fatal("changed active reasoning")
	}
	for _, row := range []int{1, 2, 3} {
		m.settingsIndex = row
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	}
	if m.visual.Mode != "auto" || m.visual.MaxPasses != 4 || m.maxToolRounds != 10 || m.settingsNote == "" {
		t.Fatal("changed locked run settings")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.showSettings || !m.input.Focused() || m.input.Value() != "keep this prompt" || m.input.Position() != 4 {
		t.Fatal("settings changed prompt state")
	}
}

func TestDialogKeysAndDeferredQuitDuringLoading(t *testing.T) {
	m := navigationModel()
	m.switchingModel = true
	m = update(t, m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if !m.showLog {
		t.Fatal("logs unavailable while loading")
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	m = next.(model)
	if !m.quitAfterSwitch || cmd != nil {
		t.Fatal("quit did not wait for loading with logs open")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	before := m.thinkMode
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.showSettings || m.thinkMode != before {
		t.Fatal("settings were writable while loading")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF1})
	m = update(t, m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	if !m.showHelp || m.showSettings || m.input.Value() != "" {
		t.Fatal("ordinary key dismissed dialog or edited prompt")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.showHelp {
		t.Fatal("escape failed to dismiss")
	}
}

func TestSavedDrawingPickerPreservesPromptAndDraft(t *testing.T) {
	m := navigationModel()
	m.height = 30
	m.workDir = t.TempDir()
	draft := filepath.Join(m.workDir, "draft.svg")
	if err := os.WriteFile(draft, []byte(visualFixture), 0600); err != nil {
		t.Fatal(err)
	}
	m.input.SetValue("unfinished prompt")
	m.input.SetCursor(3)
	m.entries = []svgEntry{{title: "Blue bird", prompt: "draw a bird"}, {title: "Red bicycle", prompt: "draw a bicycle"}}
	m.history = []ds4.ChatMessage{{Role: "user", Content: "working request"}}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3})
	m = update(t, m, tea.KeyPressMsg{Text: "bicycle"})
	if len(m.filteredDrawings()) != 1 {
		t.Fatal("search did not filter")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.showDrawings || m.entryIndex != 1 || m.input.Value() != "unfinished prompt" || m.input.Position() != 3 || !m.input.Focused() {
		t.Fatal("drawing selection lost prompt state")
	}
	if len(m.history) != 1 || m.history[0].Content != "working request" {
		t.Fatal("drawing selection replaced history")
	}
	data, err := os.ReadFile(draft)
	if err != nil || string(data) != visualFixture {
		t.Fatal("selection changed draft")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3})
	m = update(t, m, tea.KeyPressMsg{Text: "nothing matches"})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.showDrawings || m.entryIndex != 1 {
		t.Fatal("empty result selected a drawing")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.input.Position() != 3 {
		t.Fatal("cancel changed cursor")
	}
	m.generating = true
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3})
	if m.showDrawings {
		t.Fatal("opened drawings during generation")
	}
}

func TestPanelPageKeysAndVimKeys(t *testing.T) {
	m := navigationModel()
	m.input.Blur()
	m.panelFocus = focusThinking
	m.thinkBoxH = 8
	m.thinkText = strings.Repeat("activity line\n", 30)
	m.toolCalls = []toolCallEntry{{name: "svg_read", args: strings.Repeat("arguments ", 50)}}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.thinkScroll != 7 || m.thinkAutoScroll {
		t.Fatal("page key did not scroll activity")
	}
	m = update(t, m, tea.KeyPressMsg{Code: 'k', Text: "k"})
	if m.thinkScroll != 6 {
		t.Fatal("k did not scroll up")
	}
	m.panelFocus = focusTools
	m = update(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.toolScroll != 1 {
		t.Fatal("j did not scroll tools")
	}
}

func TestResizeDoesNotReloadSavedPrompt(t *testing.T) {
	m := navigationModel()
	m.height = 30
	m.entries = []svgEntry{{prompt: "old prompt"}}
	m.entryIndex = 0
	m.input.SetValue("new prompt")
	m.input.SetCursor(2)
	m = update(t, m, loadEntryMsg{})
	if m.input.Value() != "new prompt" || m.input.Position() != 2 || !m.entryLoaded {
		t.Fatal("initial load replaced in-progress prompt")
	}
	m = update(t, m, loadEntryMsg{})
	if m.input.Value() != "new prompt" || m.input.Position() != 2 {
		t.Fatal("delayed resize reload replaced prompt")
	}
}

func TestActionAvailabilityMatchesRouting(t *testing.T) {
	m := navigationModel()
	for _, busy := range []string{"opening", "generation", "release", "metadata", "switch"} {
		m.generating, m.releasingEngine, m.metadataInFlight, m.switchingModel = false, false, false, false
		m.lifecycle.status = engineinit.StatusDormant
		switch busy {
		case "opening":
			m.lifecycle.status = engineinit.StatusOpening
		case "generation":
			m.generating = true
		case "release":
			m.releasingEngine = true
		case "metadata":
			m.metadataInFlight = true
		case "switch":
			m.switchingModel = true
		}
		for _, k := range []string{"ctrl+o", "f3"} {
			a, ok := m.actions(true).Match(k)
			if !ok || a.Disabled == "" {
				t.Fatalf("%s enabled during %s", k, busy)
			}
		}
		for _, k := range []string{"f1", "f2", "ctrl+n", "ctrl+q"} {
			a, ok := m.actions(true).Match(k)
			if !ok || a.Disabled != "" {
				t.Fatalf("%s unavailable during %s", k, busy)
			}
		}
	}
	m.switchingModel = false
	m.input.Blur()
	m = update(t, m, tea.KeyPressMsg{Code: 'h', Text: "h"})
	if !m.showHelp {
		t.Fatal("help alias did not route")
	}
}

func TestDialogsFitTerminal(t *testing.T) {
	m := navigationModel()
	m.entries = []svgEntry{{title: strings.Repeat("long drawing title ", 20)}}
	for _, size := range [][2]int{{40, 15}, {80, 24}, {120, 40}} {
		m.width, m.height = size[0], size[1]
		for _, view := range []string{m.settingsOverlay(), m.drawingsOverlay(), m.helpOverlay(), m.infoOverlay()} {
			if lipgloss.Width(view) > m.width || lipgloss.Height(view) > m.height {
				t.Fatalf("dialog exceeds %dx%d: %dx%d", m.width, m.height, lipgloss.Width(view), lipgloss.Height(view))
			}
		}
	}
}

func TestScrollingBackFromTailAndDialogBottom(t *testing.T) {
	m := navigationModel()
	m.input.Blur()
	m.panelFocus = focusThinking
	m.thinkBoxH = 8
	m.thinkText = strings.Repeat("line\n", 30)
	m.thinkAutoScroll = true
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.thinkScroll != 22 || m.thinkAutoScroll {
		t.Fatalf("tail scroll jumped to %d", m.thinkScroll)
	}
	m.height = 20
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF1})
	for i := 0; i < 50; i++ {
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	bottom := m.helpScroll
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if bottom == 0 || m.helpScroll != bottom-1 {
		t.Fatal("help scrolling overshot its content")
	}
}
