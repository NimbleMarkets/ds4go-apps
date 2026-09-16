package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/charmbracelet/x/ansi"
)

// Read-only dialogs remain available while loading. Quit must still wait for
// ownership of the engine to return, even when a dialog covers the workspace.
func (m *model) dialogKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" || k == "ctrl+q" {
		if m.switchingModel {
			m.quitAfterSwitch = true
			m.statusText = "Waiting for model loading to finish before quitting…"
			return true, nil
		}
		return true, tea.Quit
	}
	if m.modelPicker.IsOpen() {
		var cmd tea.Cmd
		m.modelPicker, cmd = m.modelPicker.Update(msg)
		return true, cmd
	}
	if k == "f1" || k == "f2" || k == "ctrl+n" {
		help, settings, logs := m.showHelp, m.showSettings, m.showLog
		m.showHelp, m.showSettings, m.showLog, m.showInfo, m.showDrawings = false, false, false, false, false
		m.helpScroll = 0
		switch k {
		case "f1":
			m.showHelp = !help
		case "f2":
			m.showSettings = !settings
			m.settingsNote = ""
		case "ctrl+n":
			m.showLog = !logs
			m.logTop = -1
		}
		return true, nil
	}
	if action, ok := m.actions(m.input.Focused()).Match(k); ok && action.ID == "copy" && (m.showLog || m.showHelp || m.showInfo) {
		return true, m.copyActivePane()
	}
	if m.showSettings {
		return true, m.settingsKey(msg)
	}
	if m.showDrawings {
		return true, m.drawingsKey(msg)
	}
	if m.showLog {
		switch k {
		case "esc":
			m.showLog = false
			m.logTop = -1
		case "up":
			m.logTop = m.logScrollBy(-1)
		case "down":
			m.logTop = m.logScrollBy(1)
		case "pgup":
			m.logTop = m.logScrollBy(-m.logPageSize())
		case "pgdown":
			m.logTop = m.logScrollBy(m.logPageSize())
		}
		return true, nil
	}
	if m.showHelp || m.showInfo {
		switch k {
		case "esc":
			m.showHelp, m.showInfo = false, false
			m.helpScroll = 0
		case "up":
			m.helpScroll = max(0, m.helpScroll-1)
		case "down":
			m.helpScroll++
		case "pgup":
			m.helpScroll = max(0, m.helpScroll-max(1, m.height-6))
		case "pgdown":
			m.helpScroll += max(1, m.height-6)
		}
		lines := m.helpLines()
		if m.showInfo {
			lines = m.infoLines()
		}
		m.helpScroll = min(m.helpScroll, max(0, len(m.wrappedDialogLines(lines))-max(1, m.height-6)))
		return true, nil
	}
	return false, nil
}

// dialogView keeps headings and close hints visible on small terminals.
func (m model) dialogView(title string, lines []string, top int, hint string) string {
	if m.copyNotice != "" {
		hint = m.copyNotice
	}
	w, h := max(1, min(88, m.width-4)), max(1, m.height-6)
	wrapped := m.wrappedDialogLines(lines)
	top = min(max(0, top), max(0, len(wrapped)-h))
	body := strings.Join(wrapped[top:min(len(wrapped), top+h)], "\n")
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("75")).Padding(0, 1).
		Width(w).Render(infoHeadStyle.Render(title) + "\n" + body + "\n" + infoDimStyle.Render(ansi.Truncate(hint, w, "…")))
	return lipgloss.Place(max(1, m.width), max(1, m.height), lipgloss.Center, lipgloss.Center, box)
}

func (m *model) settingsKey(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	switch k {
	case "esc":
		m.showSettings = false
		return nil
	case "up", "shift+tab":
		m.settingsIndex = (m.settingsIndex + 4) % 5
		return nil
	case "down", "tab":
		m.settingsIndex = (m.settingsIndex + 1) % 5
		return nil
	case "left", "right", "enter", " ", "space", "ctrl+r":
	default:
		return nil
	}
	row := m.settingsIndex
	if k == "ctrl+r" {
		row = 0
	}
	m.settingsNote = ""
	if m.switchingModel || m.releasingEngine {
		m.settingsNote = "Settings are read-only until engine loading/release finishes."
		return nil
	}
	if m.modelSwitchBusy() && row != 0 && row != 4 {
		m.settingsNote = "Review and tool budgets can change when generation, enrichment, and loading finish."
		return nil
	}
	delta := 1
	if k == "left" {
		delta = -1
	}
	switch row {
	case 0:
		m.cycleReasoning()
		if delta < 0 {
			m.cycleReasoning()
		}
	case 1:
		modes := []string{"off", "auto", "on"}
		index := 0
		for i, v := range modes {
			if v == m.visual.Mode {
				index = i
			}
		}
		m.visual.Mode = modes[(index+delta+3)%3]
		if m.visual.Mode != "off" && m.engine == nil {
			ds4.ApplyVisionDefaults(&m.engOpts)
		}
		if m.visual.Mode != "off" && m.engine != nil && !m.engine.HasVision() {
			m.settingsNote = "No encoder loaded. Close settings and use Ctrl+O to load a vision model."
		}
	case 2:
		m.visual.MaxPasses = max(1, min(maxVisualPasses, m.visual.MaxPasses+delta))
	case 3:
		value := m.maxToolRounds + delta
		if validateToolRounds(value) == nil {
			m.maxToolRounds = value
		}
	case 4:
		m.preserveContext = !m.preserveContext
	}
	return nil
}

func (m model) settingsOverlay() string {
	context := "fresh for each new prompt"
	if m.preserveContext {
		context = "preserve between prompts"
	}
	values := []string{strings.TrimSpace(m.thinkModeLabel()), m.visual.Mode, fmt.Sprint(m.visual.MaxPasses), fmt.Sprint(m.maxToolRounds), context}
	labels := []string{"Reasoning", "Visual review", "Review passes", "Tool rounds / phase", "Context"}
	lines := []string{"Changes apply immediately to app settings; not saved to disk.", ""}
	for i, label := range labels {
		if m.switchingModel || m.releasingEngine || (m.modelSwitchBusy() && i > 0 && i < 4) {
			values[i] += " (locked)"
		} else if m.generating && (i == 0 || i == 4) {
			values[i] += " (next request)"
		}
		line := fmt.Sprintf("  %-20s %s", label, values[i])
		if i == m.settingsIndex {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Background(lipgloss.Color("236")).Render(">" + line[1:])
		}
		lines = append(lines, line)
	}
	encoder := "not loaded · Ctrl+O to choose/reload a vision model"
	if m.engine != nil && m.engine.HasVision() {
		encoder = "loaded"
	} else if m.engine == nil && m.engOpts.VisionPath != "" {
		encoder = "selected; loads with the engine"
	}
	lines = append(lines, "", "Model: "+m.modelDisplayName()+" · Ctrl+O in workspace", "Vision encoder: "+encoder)
	if m.generating {
		lines = append(lines, "Reasoning and context changes affect the next request.", "Review and tool budgets are locked for the current run.")
	}
	if m.switchingModel || m.releasingEngine {
		lines = append(lines, "Engine busy · settings are read-only.")
	}
	if m.settingsNote != "" {
		lines = append(lines, "", m.settingsNote)
	}
	return m.dialogView("svgpad · run settings", lines, max(0, m.settingsIndex+2-max(1, m.height-7)), "↑/↓ select · ←/→ change · Esc close")
}

func (m model) filteredDrawings() []int {
	var matches []int
	query := strings.ToLower(strings.TrimSpace(m.drawingQuery))
	for i, e := range m.entries {
		if strings.Contains(strings.ToLower(e.title+" "+e.prompt+" "+e.filename+" "+e.keywords), query) {
			matches = append(matches, i)
		}
	}
	return matches
}

func (m *model) drawingsKey(msg tea.KeyPressMsg) tea.Cmd {
	matches := m.filteredDrawings()
	switch msg.String() {
	case "esc", "f3":
		m.showDrawings = false
	case "up":
		m.drawingCursor--
	case "down":
		m.drawingCursor++
	case "pgup":
		m.drawingCursor -= max(1, m.height-10)
	case "pgdown":
		m.drawingCursor += max(1, m.height-10)
	case "enter":
		if len(matches) > 0 && !m.modelSwitchBusy() {
			m.entryIndex = matches[min(max(0, m.drawingCursor), len(matches)-1)]
			m.showDrawings = false
			// Browsing inspects a saved artifact without replacing the pending prompt
			// or the live draft/history that Continue acts on.
			value, cursor := m.input.Value(), m.input.Position()
			cmd := m.loadEntryCmd()
			m.input.SetValue(value)
			m.input.SetCursor(cursor)
			m.statusText = "Viewing saved drawing · prompt and working draft preserved"
			return cmd
		}
	case "backspace":
		r := []rune(m.drawingQuery)
		if len(r) > 0 {
			m.drawingQuery = string(r[:len(r)-1])
		}
		m.drawingCursor = 0
	case "ctrl+u":
		m.drawingQuery = ""
		m.drawingCursor = 0
	default:
		if msg.Text != "" {
			m.drawingQuery += msg.Text
			m.drawingCursor = 0
		}
	}
	m.drawingCursor = min(max(0, m.drawingCursor), max(0, len(m.filteredDrawings())-1))
	return nil
}

func (m model) drawingsOverlay() string {
	matches := m.filteredDrawings()
	lines := []string{"Search: " + m.drawingQuery, "Select to inspect. Your prompt and working draft are preserved.", ""}
	rows := max(1, m.height-11)
	top := max(0, m.drawingCursor-rows+1)
	w := max(1, min(84, m.width-8))
	for n := top; n < min(len(matches), top+rows); n++ {
		e := m.entries[matches[n]]
		title := e.title
		if title == "" {
			title = e.prompt
		}
		if title == "" {
			title = e.filename
		}
		marker := "  "
		if n == m.drawingCursor {
			marker = "> "
		}
		line := marker + ansi.Truncate(strings.Join(strings.Fields(title), " "), w, "…")
		if n == m.drawingCursor {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Background(lipgloss.Color("236")).Render(line)
		}
		lines = append(lines, line)
	}
	if len(matches) == 0 {
		lines = append(lines, "No saved drawings match.")
	}
	lines = append(lines, fmt.Sprintf("%d matching drawings", len(matches)))
	return m.dialogView("svgpad · saved drawings", lines, 0, "Type to search · ↑/↓ select · Enter view · Esc close")
}

func (m model) scrollPanel(delta int) (tea.Model, tea.Cmd) {
	thinkW := m.thinkBoxW
	if thinkW == 0 {
		thinkW = m.width * 3 / 4
	}
	thinkW = min(max(minPanelW, thinkW), m.width-minPanelW)
	bottom := func(content string, width int) int {
		wrapped := lipgloss.NewStyle().Width(max(1, width-2)).Render(content)
		return max(0, strings.Count(wrapped, "\n")+1-max(1, m.thinkBoxH))
	}
	switch m.panelFocus {
	case focusThinking:
		limit := bottom(m.activityContent(), thinkW)
		if m.thinkAutoScroll {
			m.thinkScroll = limit
		}
		m.thinkScroll = min(limit, max(0, m.thinkScroll+delta))
		m.thinkAutoScroll = m.thinkScroll == limit
	case focusTools:
		m.toolScroll = min(bottom(m.renderToolPanel(), m.width-thinkW), max(0, m.toolScroll+delta))
	case focusSVG:
		code := tea.KeyDown
		if delta < 0 {
			code = tea.KeyUp
			delta = -delta
		}
		var cmds []tea.Cmd
		for i := 0; i < delta; i++ {
			var cmd tea.Cmd
			m.svgWidget, cmd = m.svgWidget.Update(tea.KeyPressMsg{Code: code})
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m model) activityContent() string {
	var thinkContent string
	if m.thinkText != "" && m.outputText != "" {
		thinkContent = m.thinkText + "\n─────────────────\n" + m.outputText
	} else if m.thinkText != "" {
		thinkContent = m.thinkText
	} else if m.outputText != "" {
		thinkContent = m.outputText
	} else {
		if (!m.generating && m.thinkMode == ds4.ThinkNone) || (m.generating && m.activeReasoning() == ds4.ThinkNone) {
			thinkContent = "(reasoning off — Ctrl+R to change)"
		} else {
			thinkContent = "…"
		}
	}
	return thinkContent
}

func (m model) helpOverlay() string {
	return m.dialogView("svgpad · help", m.helpLines(), m.helpScroll, "↑/↓ · PgUp/PgDown scroll · Ctrl+Y copy · Esc close")
}
func (m model) infoOverlay() string {
	return m.dialogView("ds4 · info", m.infoLines(), m.helpScroll, "↑/↓ · PgUp/PgDown scroll · Ctrl+Y copy · Esc close")
}
func (m model) wrappedDialogLines(lines []string) []string {
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(line, max(1, min(88, m.width-4)), ""), "\n")...)
	}
	return wrapped
}
