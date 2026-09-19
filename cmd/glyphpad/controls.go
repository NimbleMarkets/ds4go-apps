package main

import (
	"encoding/json"
	"fmt"
	"github.com/NimbleMarkets/ntcharts/v2/canvas"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/editmode"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go-apps/internal/runconfig"
)

type chooseModelMsg struct{}

func (m model) controlActions() editmode.Actions {
	actions := padui.Globals(m.controlBusy())
	bindings := m.keymap().Command
	if m.input.Focused() {
		bindings = m.keymap().Edit
	}
	for _, b := range bindings {
		actions = append(actions, editmode.Action{ID: b.Keys, Keys: []string{b.Keys}, Label: b.Desc})
	}
	return actions
}
func (m model) settingRows() []padui.Row {
	locked := ""
	if m.loading.Active {
		locked = "Wait for engine loading to finish"
	}
	reason := runconfig.ThinkLabel(m.thinkMode)
	if m.generating && m.activeThink != m.thinkMode {
		reason = runconfig.ThinkLabel(m.activeThink) + " → " + reason + " next"
	}
	rows := []padui.Row{
		{ID: "reason", Label: "Reasoning", Value: reason, Disabled: locked},
		{ID: "temp", Label: "Temperature", Value: fmt.Sprintf("%.2f", m.runOptions.Temperature), Disabled: locked},
		{ID: "top-p", Label: "Top P", Value: fmt.Sprintf("%.2f", m.runOptions.TopP), Disabled: locked},
	}

	rows = append(rows, padui.Row{ID: "model", Label: "Model", Value: filepath.Base(m.modelPath), Disabled: "Close settings and use Ctrl+O"})
	return rows
}
func (m *model) changeSetting(change padui.Change) {
	switch change.ID {
	case "reason":
		m.thinkMode = runconfig.CycleThink(m.thinkMode, change.Delta)
	case "temp":
		m.runOptions.Temperature += float32(change.Delta) * .1
		if m.runOptions.Temperature < 0 {
			m.runOptions.Temperature = 0
		}
		if m.runOptions.Temperature > 2 {
			m.runOptions.Temperature = 2
		}
	case "top-p":
		m.runOptions.TopP += float32(change.Delta) * .05
		if m.runOptions.TopP < 0 {
			m.runOptions.TopP = 0
		}
		if m.runOptions.TopP > 1 {
			m.runOptions.TopP = 1
		}

	}
}
func (m *model) controlKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	m.controls.Notice = ""
	if m.picker.IsOpen() {
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		return true, cmd
	}
	k := msg.String()
	if k == "f1" || k == "f2" || ((k == "?" || k == "h") && !m.input.Focused() && m.controls.Kind == "") {
		kind := "help"
		if k == "f2" {
			kind = "settings"
		}
		m.showLog = false
		m.showInfo = false
		m.controls.Open(kind)
		return true, nil
	}
	if m.controls.Kind != "" {
		if k == "ctrl+y" {
			var cmd tea.Cmd
			m.controls.Notice, cmd = padui.Copy(m.controls.Kind, strings.Join(m.controls.Lines(m.settingRows(), m.controlActions()), "\n"))
			return true, cmd
		}
		if change := m.controls.Key(k, m.settingRows()); change != nil {
			m.changeSetting(*change)
		}
		m.controls.Clamp(m.width, m.height, m.settingRows(), m.controlActions())
		return true, nil
	}
	if m.showInfo {
		if k == "esc" {
			m.showInfo = false
		}
		if k == "ctrl+y" {
			var cmd tea.Cmd
			m.controls.Notice, cmd = padui.Copy("model details", fmt.Sprintf("Model: %s\nBackend: %s\nContext: %d/%d\nReasoning: %s", m.modelPath, m.backend, m.ctxPos, m.ctxSize, runconfig.ThinkLabel(m.thinkMode)))
			return true, cmd
		}
		return true, nil
	}
	if k == "ctrl+y" {
		label, text := m.copyText()
		var cmd tea.Cmd
		m.controls.Notice, cmd = padui.Copy(label, text)
		return true, cmd
	}
	if m.showLog {
		return false, nil
	}
	if a, ok := padui.Globals(m.controlBusy()).Match(k); ok {
		if a.Disabled != "" {
			m.controls.Notice = a.Disabled
			return true, nil
		}
		switch a.ID {
		case "reason":
			if !m.loading.Active {
				m.changeSetting(padui.Change{ID: "reason", Delta: 1})
			}
			return true, nil
		case "model":
			if m.lib == nil {
				m.controls.Notice = "No inference library; restart without --no-engine to select a model"
				return true, nil
			}
			return true, m.picker.Open(m.modelPath, nil)
		case "logs":
			m.showLog = true
			return true, nil
		}
	}
	if k == "tab" || k == "shift+tab" {
		return true, m.nextPane(k == "shift+tab")
	}

	return false, nil
}
func (m model) pickerView() string {
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.picker.View(m.width-4, m.height-2))
}
func (m model) controlFooter() string {
	if m.controls.Notice != "" {
		return padui.Footer(m.width, m.controls.Notice, nil)
	}
	label, _ := m.copyText()
	return padui.Footer(m.width, strings.ToUpper(label)+" · R:"+runconfig.ThinkLabel(m.thinkMode), m.controlActions().Bindings())
}
func (m *model) selectModel(info ds4.ModelInfo) tea.Cmd {
	if m.controlBusy() {
		m.controls.Notice = "Wait for current work to finish"
		return nil
	}
	opts, err := engineinit.ModelOptions(m.engOpts, info, m.mtpEnabled, false)
	if err != nil {
		m.controls.Notice = err.Error()
		return nil
	}
	m.engOpts, m.modelPath = opts, info.Path
	m.checkpoint()
	return m.loadModel()
}
func (m *model) loadModel() tea.Cmd {
	if m.loading.Active {
		return nil
	}
	if m.modelPath == "" {
		return m.picker.Open(m.modelPath, nil)
	}
	m.gen.StopAndWait()
	engine, session := m.engine, m.session
	m.engine, m.session = nil, nil
	m.engineStatus = engineinit.StatusOpening
	m.statusText = "Loading engine…"
	m.mtpPath = m.engOpts.MTPPath
	lib, opts, size := m.lib, m.engOpts, m.ctxSize
	return tea.Batch(m.loading.Start(), func() tea.Msg { return engineReadyMsg(engineinit.Switch(lib, engine, session, opts, size)) })
}

func (m model) controlBusy() bool { return m.generating || m.loading.Active || m.quitRequested }
func (m model) copyText() (string, string) {
	if m.showLog {
		if m.logBuf == nil {
			return "logs", ""
		}
		return "logs", strings.Join(m.logBuf.Lines(), "\n")
	}
	if m.input.Focused() {
		return "prompt", m.input.Value()
	}
	switch m.paneFocus {
	case 1:
		return "code", string(m.parser.Canvas)
	case 2:
		return "comment", m.descText
	case 3:
		return "activity", m.thinkText
	default:
		view := m.canvas
		view.SetCursor(canvas.Point{})
		view.ViewWidth = view.Width()
		view.ViewHeight = view.Height()
		return "canvas", view.View()
	}
}
func (m *model) nextPane(back bool) tea.Cmd {
	count := 3
	if m.showThinking {
		count = 4
	}
	index := m.paneFocus + 1
	if m.input.Focused() {
		index = 0
	}
	delta := 1
	if back {
		delta = -1
	}
	index = (index + delta + count + 1) % (count + 1)
	if index == 0 {
		return m.input.Focus()
	}
	m.input.Blur()
	m.paneFocus = index - 1
	return nil
}

// A model switch discards obsolete reasoning while keeping user requirements
// and a structured snapshot of the current drawing program.
func (m *model) checkpoint() {
	var next []chatMsg
	for _, entry := range m.history {
		if entry.role == "user" {
			next = append(next, entry)
		}
	}
	if m.parser != nil && len(m.parser.Canvas) > 0 {
		data, _ := json.Marshal(struct {
			Canvas      string `json:"canvas"`
			Description string `json:"description"`
		}{string(m.parser.Canvas), m.descText})
		next = append(next, chatMsg{"assistant", string(data)})
	}
	m.history = next
}
