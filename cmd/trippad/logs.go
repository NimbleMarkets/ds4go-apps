package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/charmbracelet/x/ansi"
)

func (m *model) loadDiagnostics() []string {
	if m.app == nil || m.app.LogBuf == nil {
		return nil
	}
	lines := m.app.LogBuf.Lines()
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] == m.loadLogMarker {
			return lines[i+1:]
		}
	}
	// If startup produced more than the ring's capacity, its retained tail is
	// still useful even though the attempt marker has been evicted.
	return lines
}

func (m *model) logLines() []string {
	lines := append([]string{"Trippad"}, m.log...)
	lines = append(lines, "", "Runtime / language-server diagnostics · current model attempt")
	native := m.loadDiagnostics()
	if len(native) == 0 {
		lines = append(lines, "No runtime diagnostics captured.")
	} else {
		lines = append(lines, native...)
	}
	var wrapped []string
	w, _ := padui.PagerSize(m.width, m.height)
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(cleanInspectText(line), w, ""), "\n")...)
	}
	return wrapped
}

func (m *model) logKey(msg tea.KeyPressMsg) bool {
	k := msg.String()
	if k == "ctrl+n" {
		m.showLog, m.logTop = !m.showLog, -1
		return true
	}
	if !m.showLog {
		return false
	}
	_, page := padui.PagerSize(m.width, m.height)
	m.logTop = padui.PagerKey(k, m.logTop, len(m.logLines()), page)
	if k == "esc" || k == "q" {
		m.showLog = false
	}
	return true
}
