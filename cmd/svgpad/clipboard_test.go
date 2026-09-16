package main

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/NimbleMarkets/ds4go/dsml"
)

func copyKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl} }

func TestCopyPromptPreservesEditorAndRunState(t *testing.T) {
	for _, busy := range []string{"idle", "generating", "switching"} {
		m := navigationModel()
		m.input.SetValue("a 日本語 prompt\n")
		m.input.SetCursor(2)
		m.generating = busy == "generating"
		m.switchingModel = busy == "switching"
		m.statusText = "Working"
		next, cmd := m.Update(copyKey())
		got := next.(model)
		if cmd == nil || !reflect.DeepEqual(cmd(), tea.SetClipboard(m.input.Value())()) {
			t.Fatalf("wrong clipboard command during %s", busy)
		}
		if got.input.Value() != m.input.Value() || got.input.Position() != 2 || !got.input.Focused() || got.statusText != "Working" || got.yoloMode {
			t.Fatal("copy changed editor or run state")
		}
		if !strings.Contains(got.footerText(), "Sent prompt") {
			t.Fatal("missing copy feedback")
		}
		got = update(t, got, tea.KeyPressMsg{Code: 'a', Text: "a"})
		if got.copyNotice != "" {
			t.Fatal("copy notice survived next key")
		}
	}
}

func TestCopyPaneContentWithoutStylingOrViewportClipping(t *testing.T) {
	for _, pane := range []panelFocus{focusSVG, focusThinking, focusTools} {
		m := navigationModel()
		m.input.Blur()
		m.panelFocus = pane
		m.width = 20
		m.thinkBoxH = 2
		m.thinkText = "\x1b[31mreasoning\x1b[0m"
		m.outputText = strings.Repeat("complete output\n", 20)
		m.thinkScroll = 10
		m.toolScroll = 10
		m.toolCalls = []toolCallEntry{{name: "svg_read", args: "{}", result: "complete result"}}
		m.displayedSVG = visualFixture
		next, cmd := m.Update(copyKey())
		got := next.(model)
		if cmd == nil {
			t.Fatal("missing copy command")
		}
		text := reflect.ValueOf(cmd()).String()
		if strings.Contains(text, "\x1b") {
			t.Fatal("copied terminal escape sequences")
		}
		switch pane {
		case focusSVG:
			if text != visualFixture {
				t.Fatal("did not copy SVG source")
			}
		case focusThinking:
			if !strings.HasPrefix(text, "reasoning") || !strings.HasSuffix(text, m.outputText) {
				t.Fatal("activity copied only viewport")
			}
		case focusTools:
			if !strings.Contains(text, "svg_read") || !strings.Contains(text, "complete result") {
				t.Fatal("missing tool content")
			}
		}
		if got.panelFocus != pane || got.thinkScroll != 10 || got.toolScroll != 10 {
			t.Fatal("copy changed navigation")
		}
	}
}

func TestCopySVGTracksStreamedAndSelectedPreview(t *testing.T) {
	m := navigationModel()
	m.input.Blur()
	m.panelFocus = focusSVG
	m.entries = []svgEntry{{filename: "saved.svg", svgData: []byte(`<svg><text>saved drawing</text></svg>`)}}
	m.loadEntryCmd()
	_, text := m.activePaneText()
	if text != string(m.entries[0].svgData) {
		t.Fatal("selected entry not copied")
	}
	m.generating = true
	m.applyStreamEvent(dsml.StreamEvent{Type: dsml.EventContentDelta, Delta: "<svg><text>new live drawing</text></svg>\n"})
	_, text = m.activePaneText()
	if !strings.Contains(text, "new live drawing") || strings.Contains(text, "saved drawing") {
		t.Fatal("copied old entry during live preview")
	}
	m.generating = false // stopped generation must keep the visible preview source
	_, text = m.activePaneText()
	if !strings.Contains(text, "new live drawing") {
		t.Fatal("stop reverted clipboard source")
	}
	m.loadEntryCmd()
	_, text = m.activePaneText()
	if text != string(m.entries[0].svgData) {
		t.Fatal("browsing copied the live draft")
	}
}

func TestCopyReadOnlyDialogsTargetsOverlay(t *testing.T) {
	for _, dialog := range []string{"logs", "help", "info"} {
		m := navigationModel()
		m.input.SetValue("hidden prompt")
		m.input.SetCursor(3)
		m.logBuf = ds4log.NewBuffer(10)
		m.logBuf.Write([]byte("first log\nlast log\n"))
		switch dialog {
		case "logs":
			m.showLog = true
			m.switchingModel = true
		case "help":
			m.showHelp = true
		case "info":
			m.showInfo = true
		}
		_, want := m.activePaneText()
		next, cmd := m.Update(copyKey())
		got := next.(model)
		if cmd == nil {
			t.Fatalf("no copy for %s", dialog)
		}
		if text := reflect.ValueOf(cmd()).String(); strings.Contains(text, "hidden prompt") || text == "" {
			t.Fatalf("copied underlying prompt for %s", dialog)
		}
		if want == "" || got.input.Position() != 3 || !got.input.Focused() || got.showLog != m.showLog || got.showHelp != m.showHelp || got.showInfo != m.showInfo {
			t.Fatal("copy dismissed dialog or changed editor")
		}
	}
}

func TestEmptyPaneDoesNotClearClipboard(t *testing.T) {
	m := navigationModel()
	next, cmd := m.Update(copyKey())
	got := next.(model)
	if cmd != nil || !strings.Contains(got.copyNotice, "Nothing to copy") {
		t.Fatal("empty pane issued a clipboard write")
	}
	m.input.Blur()
	m.panelFocus = focusThinking
	_, cmd = m.Update(copyKey())
	if cmd != nil {
		t.Fatal("copied empty activity placeholder")
	}
}
