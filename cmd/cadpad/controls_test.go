package main

import (
	"context"
	"io"
	"log"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
)

func controlsModel() model {
	input := textinput.New()
	input.Focus()
	return model{input: input, logger: log.New(io.Discard, "", 0), maxRounds: 20}
}
func TestPromptQDoesNotQuitAndControlsPreserveCursor(t *testing.T) {
	m := controlsModel()
	m, cmd := m.handleKeyMsg(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.quitRequested || m.input.Value() != "q" {
		t.Fatal("typing q quit")
	}
	_ = cmd
	m.input.SetValue("a box")
	m.input.SetCursor(2)
	m.inferencing = true
	m, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.thinkMode != ds4.ThinkHigh || m.activeThink != ds4.ThinkNone {
		t.Fatal("reasoning changed active run")
	}
	m, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyF2})
	m, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.input.Value() != "a box" || m.input.Position() != 2 || !m.input.Focused() {
		t.Fatal("settings changed prompt")
	}
	m, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.input.Value() != "a box" {
		t.Fatal("busy submit consumed prompt")
	}
}
func TestEscapeCancelsOwnedGeneration(t *testing.T) {
	m := controlsModel()
	m.inferencing = true
	m.gen, _ = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		<-ctx.Done()
		ch <- toolDoneMsg{err: ctx.Err()}
	})
	m, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.gen.Canceled() {
		t.Fatal("escape did not cancel")
	}
	m.gen.StopAndWait()
}
func TestSettingsAndLoadingGuard(t *testing.T) {
	m := controlsModel()
	m.loading = padui.Loading{Active: true}
	m.opening = true
	m, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if m.picker.IsOpen() {
		t.Fatal("opened picker during loading")
	}
	m, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.thinkMode != ds4.ThinkNone {
		t.Fatal("changed locked settings")
	}
	next, cmd := m.handleKeyMsg(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd != nil || !next.quitRequested {
		t.Fatal("quit failed to wait for loading")
	}
}

func TestModalIgnoresMouseAndPanelKeysDoNotSelectObjects(t *testing.T) {
	m := controlsModel()
	m.controls.Open("settings")
	next, _ := m.Update(tea.MouseWheelMsg{Y: 5})
	if next.(model).camZoom != m.camZoom {
		t.Fatal("modal changed camera")
	}
	m.controls.Kind = ""
	m.input.Blur()
	m.focus = focusThinking
	m, _ = m.handleKeyMsg(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if m.thinkScroll != 1 {
		t.Fatal("k did not scroll focused activity")
	}
}
