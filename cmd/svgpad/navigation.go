package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/editmode"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/charmbracelet/x/ansi"
)

func (m model) defaultPanelFocus() panelFocus {
	if m.showThinking {
		return focusThinking
	}
	return focusSVG
}

func (m *model) captureRequestReasoning() {
	m.requestThinkMode, m.requestThinkSet = m.thinkMode, true
}

func (m model) activeReasoning() ds4.ThinkMode {
	if m.requestThinkSet {
		return m.requestThinkMode
	}
	return m.thinkMode
}

func (m *model) cycleReasoning() {
	if m.generating && !m.requestThinkSet {
		m.captureRequestReasoning()
	}
	switch m.thinkMode {
	case ds4.ThinkNone:
		m.thinkMode = ds4.ThinkHigh
	case ds4.ThinkHigh:
		m.thinkMode = ds4.ThinkMax
	default:
		m.thinkMode = ds4.ThinkNone
	}
	m.logger.Printf("[REASONING] configured=%s next-request=%v", strings.TrimSpace(m.thinkModeLabel()), m.generating)
}

func (m model) settingsStrip() string {
	if m.width <= 0 {
		return ""
	}
	reason := strings.TrimSpace(m.thinkModeLabel())
	if m.generating {
		active := m
		active.thinkMode = m.activeReasoning()
		if active.thinkMode != m.thinkMode {
			reason = strings.TrimSpace(active.thinkModeLabel()) + " → " + reason + " next"
		}
	}
	reasonKey, modelKey := "Ctrl+R ", "Ctrl+O "
	if m.switchingModel {
		reasonKey = ""
	}
	if m.modelSwitchBusy() {
		modelKey = ""
	}
	settings := fmt.Sprintf(" %sReason:%s │ %sModel:%s │ Vision:%s │ Reviews:%d │ Tools:%d",
		reasonKey, reason, modelKey, m.modelDisplayName(), m.visual.Mode, m.visual.MaxPasses, m.maxToolRounds)
	return lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Width(m.width).Render(ansi.Truncate(settings, m.width, "…"))
}

// actions is the common source for dispatch, footer hints, and help. Bare
// letters belong to panels; the editor keeps them for text entry.
func (m model) actions(editing bool) editmode.Actions {
	a := editmode.Actions{}
	add := func(id, label, group, disabled string, keys ...string) {
		a = append(a, editmode.Action{ID: id, Keys: keys, Label: label, Group: group, Disabled: disabled})
	}
	idle := ""
	if m.modelSwitchBusy() {
		idle = "Wait for generation, enrichment, or engine loading/release to finish"
	}
	loading := ""
	if m.switchingModel {
		loading = "Wait for model loading to finish"
	}
	run := loading
	if m.generating || m.releasingEngine {
		run = "Wait for the current run or engine release to finish"
	}
	if editing {
		submit := "generate"
		if m.lifecycle.status == engineinit.StatusOpening {
			submit = "queue prompt"
		}
		if m.needsVisionModelSelection() {
			submit = "choose model"
		}
		add("enter", submit, "Prompt", run, "enter")
	} else {
		add("c", "continue/fix", "Prompt", idle, "c")
		add("e", "prompt", "Prompt", loading, "e")
		add("n", "new", "Prompt", idle, "n")
	}
	esc := "panels"
	reason := "reasoning"
	if m.generating {
		esc, reason = "stop", "next reasoning"
	}
	add("esc", esc, "Workspace", loading, "esc")
	add("ctrl+r", reason, "Run", loading, "ctrl+r")
	add("f1", "help", "Workspace", "", "f1")
	add("copy", "copy pane", "Workspace", "", "ctrl+y")
	add("f2", "settings", "Run", "", "f2")
	add("f3", "drawings", "Workspace", idle, "f3")
	add("tab", "panels", "Workspace", loading, "tab")
	add("shift+tab", "previous panel", "Workspace", loading, "shift+tab")
	add("ctrl+o", "model", "Run", idle, "ctrl+o")
	add("ctrl+n", "logs", "Workspace", "", "ctrl+n")
	quit := "quit"
	if m.switchingModel {
		quit = "quit after loading"
	}
	add("quit", quit, "Workspace", "", "ctrl+q", "ctrl+c")
	if !editing {
		add("ctrl+r", reason, "Run", loading, "r")
		add("f1", "help", "Workspace", loading, "?", "h")
		add("m", "model details", "Inspect", loading, "m")
		add("t", "activity panels", "Inspect", loading, "t")
		add("i", "drawing details", "Inspect", loading, "i")
		add("g", "render mode", "Inspect", loading, "g")
		add("[", "fewer reviews", "Run", idle, "[")
		add("]", "more reviews", "Run", idle, "]")
		add("p", "preserve context", "Run", loading, "p")
		add("y", "auto prompts", "Run", loading, "y")
		add("x", "release engine", "Run", loading, "x")
		add("<", "less GPU power", "Run", loading, "<", ",")
		add(">", "more GPU power", "Run", loading, ">", ".")
		add("up", "scroll/pan up", "Panel", loading, "up", "k")
		add("down", "scroll/pan down", "Panel", loading, "down", "j")
		add("pgup", "page up", "Panel", loading, "pgup")
		add("pgdown", "page down", "Panel", loading, "pgdown")
		if m.panelFocus == focusSVG {
			add("left", "pan left", "Panel", loading, "left")
			add("right", "pan right", "Panel", loading, "right")
			add("+", "zoom in", "Panel", loading, "+", "=")
			add("-", "zoom out", "Panel", loading, "-")
			add("0", "reset view", "Panel", loading, "0")
		}
	}
	add("shift+up", "shorter panels", "Layout", loading, "shift+up", "ctrl+minus", "ctrl+-")
	add("shift+down", "taller panels", "Layout", loading, "shift+down", "ctrl+plus", "ctrl+=")
	add("shift+left", "narrower activity", "Layout", loading, "shift+left")
	add("shift+right", "wider activity", "Layout", loading, "shift+right")
	return a
}

func (m model) keymap() editmode.Keymap {
	return editmode.Keymap{Edit: m.actions(true).Bindings(), Command: m.actions(false).Bindings()}
}

func (m model) footerText() string {
	if m.width <= 0 {
		return ""
	}
	if m.copyNotice != "" {
		return ansi.Truncate(" "+m.copyNotice, m.width, "…")
	}
	keymap := m.keymap()
	bindings := keymap.Command
	label := "SVG"
	if m.panelFocus == focusThinking {
		label = "ACTIVITY"
	}
	if m.panelFocus == focusTools {
		label = "TOOLS"
	}
	if m.input.Focused() {
		bindings, label = keymap.Edit, "PROMPT"
	}
	if m.switchingModel {
		label = "LOADING"
	}
	if m.releasingEngine {
		label = "RELEASING"
	}
	prefix := " " + label + " │ "
	text := ""
	for i := range bindings {
		candidate := (editmode.Keymap{Command: bindings[:i+1]}).FooterText(false)
		if lipgloss.Width(prefix+candidate) > m.width {
			break
		}
		text = candidate
	}
	if text == "" && len(bindings) > 0 {
		text = (editmode.Keymap{Command: bindings[:1]}).FooterText(false)
	}
	return ansi.Truncate(prefix+text, m.width, "…")
}
