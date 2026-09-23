package main

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
)

func TestQwenPowerOnStartupAndSwitch(t *testing.T) {
	models := pickerModels(t)
	qwen := models["qwen38-q2"]
	for _, direct := range []bool{false, true} {
		m := pickerModel(t, qwen)
		m.app.EngineOpts.PowerPercent = 80
		m.app.EngineOpts.SSDStreaming = false
		if direct {
			m.app.Flags.Model = qwen.Alias
			finishLoad(t, m, m.startupModel())
		} else {
			loadFixture(t, m, models["q2-imatrix"])
			loadFixture(t, m, qwen)
		}
		if m.engOpts.PowerPercent != 100 || m.app.EngineOpts.PowerPercent != 80 {
			t.Fatal("Qwen throttle wasn't disabled without changing the user's base options")
		}
		if m.engOpts.ContextSize != 256 || m.engOpts.PlacementCtxHint != 256 {
			t.Fatal("engine allocation doesn't match the session context")
		}
		if !strings.Contains(strings.Join(m.log, "\n"), "does not support power throttling") {
			t.Fatal("missing explanation of power adjustment")
		}
		loadFixture(t, m, models["q2-imatrix"])
		if m.engOpts.PowerPercent != 80 {
			t.Fatal("switching back lost configured power")
		}
		m.close()
	}
}

func TestFailureDialogReadsLateNativeDiagnosticsAndPreservesPrompt(t *testing.T) {
	models := pickerModels(t)
	m := pickerModel(t, models["qwen38-q2"])
	m.modelPath = models["qwen38-q2"].Path
	m.modelInfo = m.app.ModelInfo
	m.app.LogBuf = ds4log.NewBuffer(500)
	m.loadLogMarker = "current attempt"
	m.app.LogBuf.Write([]byte("old failure\ncurrent attempt\n"))
	m.input.Focus()
	m.input.SetValue("keep this prompt")
	m.input.SetCursor(4)
	m.Update(engineMsg{result: engineinit.Result{Err: errors.New("ds4 status 1")}})
	if !m.showLog || !strings.Contains(m.status, "Ctrl+N") {
		t.Fatal("failure details aren't discoverable")
	}
	// The native pipe reader can deliver its final line after engineMsg.
	m.app.LogBuf.Write([]byte("ds4: power throttling is not supported\n"))
	view := m.View().Content
	if !strings.Contains(view, "power throttling is not supported") || strings.Contains(view, "old failure") {
		t.Fatal("dialog lost current native error or shows stale startup errors")
	}
	m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.showLog || m.input.Value() != "keep this prompt" || !m.input.Focused() || m.input.Position() != 4 {
		t.Fatal("log dialog changed prompt or focus")
	}
	m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if !m.showLog {
		t.Fatal("Ctrl+N doesn't reopen logs while editing prompt")
	}
	for i := 0; i < 30; i++ {
		m.app.LogBuf.Write([]byte(strings.Repeat("long diagnostic ", 20) + "\n"))
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.logTop < 0 {
		t.Fatal("can't page through wrapped diagnostics")
	}
	top := m.logTop
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.logTop != top-1 {
		t.Fatal("up doesn't scroll one wrapped line")
	}
	m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if m.showLog {
		t.Fatal("Ctrl+N doesn't close logs")
	}
}

func TestWideLogPagerRetainsPositionWhileAppending(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 180, 24
	m.log = []string{strings.Repeat("x", 140)}
	if len(m.logLines()) != 5 {
		t.Fatal("log still wraps at narrow dialog width")
	}
	for i := 0; i < 80; i++ {
		m.log = append(m.log, "another diagnostic")
	}
	m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	top := m.logTop
	m.log = append(m.log, "new diagnostic")
	if top < 0 || m.logTop != top || !strings.Contains(m.View().Content, "more below") {
		t.Fatal("new output stole pager position")
	}
	m.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	if m.logTop != -1 || !strings.Contains(m.View().Content, "following") {
		t.Fatal("G didn't resume follow")
	}
}
