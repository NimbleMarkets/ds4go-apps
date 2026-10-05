package main

import (
	"context"
	"io"
	"log"
	"reflect"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ntcharts/v2/canvas"
)

func controlsModel() model {
	input := textinput.New()
	input.Focus()
	return model{input: input, parser: NewParser(), canvas: canvas.New(20, 10), engineStatus: engineinit.StatusDormant, logger: log.New(io.Discard, "", 0), width: 120, height: 30, stepN: -1}
}
func press(m model, k tea.KeyPressMsg) model { next, _ := m.Update(k); return next.(model) }
func TestControlsPreserveTypingAndActiveReasoning(t *testing.T) {
	m := controlsModel()
	m.input.SetValue("drawing prompt")
	m.input.SetCursor(3)
	m.generating = true
	m.activeThink = ds4.ThinkNone
	m = press(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.thinkMode != ds4.ThinkHigh || m.activeThink != ds4.ThinkNone {
		t.Fatal("reasoning did not target next request")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF2})
	if m.controls.Kind != "settings" {
		t.Fatal("settings inaccessible while typing")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.input.Focused() || m.input.Position() != 3 || m.input.Value() != "drawing prompt" {
		t.Fatal("settings lost prompt state")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if cmd == nil || !reflect.DeepEqual(cmd(), tea.SetClipboard("drawing prompt")()) {
		t.Fatal("wrong copy command")
	}
}
func TestVisibleFocusAndActiveResetGuard(t *testing.T) {
	m := controlsModel()
	m.input.SetValue("keep")
	for _, show := range []bool{false, true} {
		m.showThinking = show
		for i := 0; i < 10; i++ {
			m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
			if !show && m.paneFocus == 3 && !m.input.Focused() {
				t.Fatal("hidden focus")
			}
		}
	}
	m.generating = true
	m.input.Blur()
	m = press(m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.input.Value() != "keep" {
		t.Fatal("reset live prompt")
	}
}
func TestQuitCancelsWorkerAndWaitsForOpening(t *testing.T) {
	m := controlsModel()
	m.generating = true
	m.gen, _ = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) { defer close(ch); <-ctx.Done(); ch <- bubble.DoneMsg{} })
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd == nil || !m.gen.Canceled() {
		t.Fatal("quit failed to cancel worker")
	}
	m.gen.StopAndWait()
	m = controlsModel()
	m.engineStatus = engineinit.StatusOpening
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd != nil || !m.quitRequested {
		t.Fatal("quit did not wait for engine ownership")
	}
	_, cmd = m.Update(engineReadyMsg{Err: context.Canceled})
	if cmd == nil {
		t.Fatal("quit lost after opening error")
	}
}

func TestInfoDialogPreservesFocus(t *testing.T) {
	m := controlsModel()
	m.showInfo = true
	m.input.SetValue("keep")
	m.input.SetCursor(2)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if !m.showInfo || !m.input.Focused() || m.input.Position() != 2 {
		t.Fatal("dialog changed underlying focus")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.showInfo {
		t.Fatal("dialog did not close")
	}
}

func TestSecondCtrlCForcesQuitDuringOpening(t *testing.T) {
	m := controlsModel()
	m.engineStatus = engineinit.StatusOpening
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m = next.(model); cmd != nil || m.forceQuit {
		t.Fatal("first ctrl+c did not wait for opening")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m = next.(model); cmd == nil || !m.forceQuit {
		t.Fatal("second ctrl+c did not force quit")
	}
}
