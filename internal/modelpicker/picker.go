// Package modelpicker provides an embeddable Bubble Tea picker for installed
// ds4go chat models. Hosts handle SelectedMsg and own engine switching.
package modelpicker

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
)

// SelectedMsg requests a model change; no engine or global default is changed.
type SelectedMsg struct{ Model ds4.ModelInfo }

// LoadedMsg carries an asynchronous catalog snapshot back to the picker.
type LoadedMsg struct {
	models  []ds4.ModelInfo
	err     error
	request uint64
	current string
}

// Model is a modal picker. Its zero value is ready to use.
type Model struct {
	open, loading       bool
	request             uint64
	models              []ds4.ModelInfo
	current, query, err string
	cursor              int
	unavailable         func(ds4.ModelInfo) string
}

// IsOpen reports whether the host should route keys and render the picker.
func (m Model) IsOpen() bool { return m.open }

// Open refreshes the local catalog without downloading or opening a model.
// unavailable optionally explains why a model cannot be selected in this app.
func (m *Model) Open(current string, unavailable func(ds4.ModelInfo) string) tea.Cmd {
	m.open, m.loading = true, true
	m.query, m.err, m.cursor = "", "", 0
	m.models = nil
	m.current, m.unavailable = current, unavailable
	m.request++
	request := m.request
	return func() tea.Msg {
		models, err := ds4.ListModels()
		if info, ok := ds4.ResolveModelInfo(current); ok {
			current = info.Path // normalize the default hardlink to its catalog identity
		} else if st, statErr := os.Stat(current); statErr == nil && st.Mode().IsRegular() && st.Size() > 0 {
			models = append(models, ds4.ModelInfo{Alias: filepath.Base(current), Path: current, Family: "custom", Installed: true})
		}
		return LoadedMsg{models: models, err: err, request: request, current: current}
	}
}

func (m Model) matches() []ds4.ModelInfo {
	var out []ds4.ModelInfo
	for _, info := range m.models {
		if info.Installed && info.IsChatModel() && strings.Contains(strings.ToLower(info.Alias+" "+info.Family+" "+info.FileName), strings.ToLower(m.query)) {
			out = append(out, info)
		}
	}
	return out
}

// Update handles catalog results and keys. Escape cancels; Enter returns a
// command delivering SelectedMsg. Other application messages need not be routed.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.open {
		return m, nil
	}
	switch msg := msg.(type) {
	case LoadedMsg:
		if msg.request != m.request {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.models, m.current = msg.models, msg.current
		for i, info := range m.matches() {
			if info.Path == m.current {
				m.cursor = i
				break
			}
		}
	case tea.KeyPressMsg:
		items := m.matches()
		switch msg.String() {
		case "esc":
			m.open = false
		case "up", "ctrl+p":
			m.cursor = max(0, m.cursor-1)
		case "down", "ctrl+n":
			m.cursor = min(max(0, len(items)-1), m.cursor+1)
		case "pgup":
			m.cursor = max(0, m.cursor-8)
		case "pgdown":
			m.cursor = min(max(0, len(items)-1), m.cursor+8)
		case "enter":
			if m.loading || len(items) == 0 {
				return m, nil
			}
			info := items[m.cursor]
			if m.unavailable != nil {
				if reason := m.unavailable(info); reason != "" {
					m.err = reason
					return m, nil
				}
			}
			m.open = false
			return m, func() tea.Msg { return SelectedMsg{Model: info} }
		case "backspace", "ctrl+h":
			runes := []rune(m.query)
			if len(runes) > 0 {
				m.query = string(runes[:len(runes)-1])
			}
			m.cursor, m.err = 0, ""
		case "ctrl+u":
			m.query, m.cursor, m.err = "", 0, ""
		default:
			if msg.Text != "" {
				m.query += msg.Text
				m.cursor, m.err = 0, ""
			}
		}
	}
	return m, nil
}
