package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Keep the preview source with the view, including streamed previews and cached
// drawings. Reading draft.svg here could copy a different drawing than the one
// being inspected, or race with a tool writing the file.
func (m *model) setPreviewSVG(name string, data []byte) tea.Cmd {
	m.displayedSVG = string(data)
	return m.svgWidget.SetSVGData(name, data)
}

func (m model) activePaneText() (label, text string) {
	switch {
	case m.showLog:
		if m.logBuf == nil {
			return "logs", ""
		}
		return "logs", strings.Join(m.logBuf.Lines(), "\n")
	case m.showHelp:
		return "help", strings.Join(m.helpLines(), "\n")
	case m.showInfo:
		return "model details", strings.Join(m.infoLines(), "\n")
	case m.input.Focused():
		return "prompt", m.input.Value()
	}
	switch m.panelFocus {
	case focusSVG:
		return "SVG source", m.displayedSVG
	case focusThinking:
		if m.thinkText == "" && m.outputText == "" {
			return "activity", ""
		}
		return "activity", m.activityContent()
	case focusTools:
		return "tools", m.renderToolPanel()
	}
	return "pane", ""
}

func (m *model) copyActivePane() tea.Cmd {
	label, text := m.activePaneText()
	text = ansi.Strip(text)
	if strings.TrimSpace(text) == "" {
		m.copyNotice = "Nothing to copy from " + label
		return nil
	}
	// OSC 52 addresses the user's terminal clipboard, including over SSH. There
	// is no acknowledgement, so report sending rather than confirmed delivery.
	m.copyNotice = "Sent " + label + " to terminal clipboard"
	return tea.SetClipboard(text)
}
