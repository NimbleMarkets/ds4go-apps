package main

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
)

func navigationModel() model {
	m := testModel()
	m.input = textinput.New()
	m.input.Focus()
	m.width = 120
	m.visual = visualOptions{Mode: "auto", MaxPasses: 3}
	return m
}

func TestReasoningShortcutPreservesPromptAndCursor(t *testing.T) {
	m := navigationModel()
	m.input.SetValue("abcd")
	m.input.SetCursor(2)
	for _, want := range []ds4.ThinkMode{ds4.ThinkHigh, ds4.ThinkMax, ds4.ThinkNone} {
		m = update(t, m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
		if m.thinkMode != want || !m.input.Focused() || m.input.Value() != "abcd" || m.input.Position() != 2 {
			t.Fatal("reasoning shortcut changed prompt state")
		}
	}
	m = update(t, m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	if m.input.Value() != "abrcd" || m.thinkMode != ds4.ThinkNone {
		t.Fatal("plain r should type in editor")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = update(t, m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	if m.thinkMode != ds4.ThinkHigh {
		t.Fatal("command-mode r alias lost")
	}
}

func TestTypingDuringGenerationKeepsNavigationLetters(t *testing.T) {
	m := navigationModel()
	m.generating = true
	m.entries = []svgEntry{{prompt: "one"}, {prompt: "two"}}
	m.entryIndex = 1
	for _, r := range "jkrn" {
		m = update(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.input.Value() != "jkrn" || m.entryIndex != 1 {
		t.Fatal("navigation stole editor input")
	}
	m.input.Blur()
	m = update(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.input.Value() != "jkrn" || !m.generating {
		t.Fatal("new prompt reset active work")
	}
}

func TestEscapeAndTabOnlyFocusVisiblePanels(t *testing.T) {
	for _, show := range []bool{false, true} {
		m := navigationModel()
		m.showThinking = show
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
		if m.input.Focused() || (!show && m.panelFocus != focusSVG) || (show && m.panelFocus != focusThinking) {
			t.Fatal("escape focused hidden panel")
		}
		for i := 0; i < 8; i++ {
			m = update(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
			if !show && m.panelFocus != focusSVG && m.panelFocus != focusInput {
				t.Fatal("tab focused hidden panel")
			}
			if m.input.Focused() != (m.panelFocus == focusInput) {
				t.Fatal("focus states diverged")
			}
		}
	}
}

func TestReasoningChangeDuringRequestIsDeferred(t *testing.T) {
	m := navigationModel()
	m.captureRequestReasoning()
	m.generating = true
	m = update(t, m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.activeReasoning() != ds4.ThinkNone || m.thinkMode != ds4.ThinkHigh {
		t.Fatal("changed active request reasoning")
	}
	if !strings.Contains(m.settingsStrip(), "OFF → HIGH next") {
		t.Fatalf("missing active/next distinction: %s", m.settingsStrip())
	}
	// Automatic retries use activeReasoning without taking a new snapshot.
	m.autoCorrectCount++
	if m.activeReasoning() != ds4.ThinkNone {
		t.Fatal("retry changed reasoning")
	}
	m.generating = false
	m.captureRequestReasoning()
	if m.activeReasoning() != ds4.ThinkHigh {
		t.Fatal("next request did not adopt reasoning")
	}
}

func TestFooterAvailabilityAndPromptSettings(t *testing.T) {
	m := navigationModel()
	m.width = 88
	footer := m.footerText()
	for _, text := range []string{"PROMPT", "enter", "ctrl+r", "f1"} {
		if !strings.Contains(footer, text) {
			t.Fatalf("missing %s in %s", text, footer)
		}
	}
	for _, text := range []string{"Reason:", "Model:", "Vision:", "Reviews:"} {
		if !strings.Contains(m.settingsStrip(), text) {
			t.Fatalf("missing setting %s", text)
		}
	}
	m.generating = true
	bindings := m.keymap().FooterText(true)
	if strings.Contains(bindings, "enter") || strings.Contains(bindings, "ctrl+o") || !strings.Contains(bindings, "stop") || !strings.Contains(bindings, "next reasoning") {
		t.Fatalf("incorrect generation footer: %s", bindings)
	}
	m.switchingModel = true
	bindings = m.keymap().FooterText(true)
	if strings.Contains(bindings, "ctrl+r") || strings.Contains(bindings, "ctrl+o") || !strings.Contains(bindings, "quit after loading") {
		t.Fatalf("incorrect switching footer: %s", bindings)
	}
	for _, width := range []int{1, 20, 80, 120} {
		m.width = width
		for _, line := range []string{m.footerText(), m.settingsStrip()} {
			if lipgloss.Width(line) > width || strings.Contains(line, "\n") {
				t.Fatal("settings/footer wraps")
			}
		}
	}
}

func TestHelpAccessibleWhileTyping(t *testing.T) {
	m := navigationModel()
	m.input.SetValue("draft prompt")
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF1})
	if !m.showHelp || m.input.Value() != "draft prompt" || !m.input.Focused() {
		t.Fatal("help unavailable while typing")
	}
}
