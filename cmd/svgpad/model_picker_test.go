package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/modelpicker"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go/ds4api"
)

func installedPickerModels(t *testing.T) map[string]ds4.ModelInfo {
	t.Helper()
	t.Setenv("DS4_DIR", t.TempDir())
	if err := os.MkdirAll(ds4.DefaultModelsDir(), 0755); err != nil {
		t.Fatal(err)
	}
	list, err := ds4.ListModels()
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range list {
		switch info.Alias {
		case "glm53-q2", "glm53-vision", "v41-q2", "v41-vision", "q2-imatrix":
			if err := os.WriteFile(info.Path, []byte("fixture"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	list, err = ds4.ListModels()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]ds4.ModelInfo)
	for _, info := range list {
		out[info.Alias] = info
	}
	return out
}

func TestSwitchRebindsCompanionsAndPreservesRuntimeSettings(t *testing.T) {
	models := installedPickerModels(t)
	base := ds4.EngineOptions{ModelPath: models["v41-q2"].Path, VisionPath: models["v41-vision"].Path, MTPPath: "old-drafter.gguf", Backend: ds4.BackendMetal, SSDStreaming: true, ContextSize: 16384, PowerPercent: 65, WarmWeights: true}
	next, err := switchedModelOptions(base, models["glm53-q2"], "on", true)
	if err != nil {
		t.Fatal(err)
	}
	if next.ModelPath != models["glm53-q2"].Path || next.VisionPath != models["glm53-vision"].Path || next.MTPPath != "" {
		t.Fatalf("wrong companions: %+v", next)
	}
	if next.Backend != base.Backend || !next.SSDStreaming || next.ContextSize != base.ContextSize || next.PowerPercent != base.PowerPercent || !next.WarmWeights {
		t.Fatal("lost runtime settings")
	}
	off, err := switchedModelOptions(base, models["glm53-q2"], "off", false)
	if err != nil || off.VisionPath != "" || off.MTPPath != "" {
		t.Fatal("disabled companions carried over")
	}
	if _, err := switchedModelOptions(base, models["q2-imatrix"], "on", false); err == nil {
		t.Fatal("required vision silently disabled")
	}
	if err := os.WriteFile(models["glm53-vision"].Path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := switchedModelOptions(base, models["glm53-q2"], "on", false); err == nil {
		t.Fatal("missing encoder accepted")
	}
	if err := os.WriteFile(models["glm53-q2"].Path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := switchedModelOptions(base, models["glm53-q2"], "auto", false); err == nil {
		t.Fatal("stale installed state accepted")
	}
}

func TestSwitchEngineLifecyclePreservesDraftOnSuccessAndFailure(t *testing.T) {
	models := installedPickerModels(t)
	for _, fail := range []bool{false, true} {
		m := testModel()
		m.workDir = t.TempDir()
		m.ctxSize = 8192
		m.visual = visualOptions{Mode: "auto", MaxPasses: 3}
		m.lib = ds4api.NewMockLibrary()
		oldEng, err := m.lib.NewEngine(ds4.EngineOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer oldEng.Close()
		oldSess, err := oldEng.NewSession(m.ctxSize)
		if err != nil {
			t.Fatal(err)
		}
		defer oldSess.Close()
		m.engine, m.session = oldEng, oldSess
		m.lifecycle.status = engineinit.StatusReady
		m.modelPath = models["v41-q2"].Path
		m.history = []ds4.ChatMessage{{Role: "user", Content: "draw a pelican"}, {Role: "assistant", Content: "old model answer"}}
		path := filepath.Join(m.workDir, "draft.svg")
		if err := os.WriteFile(path, []byte(visualFixture), 0644); err != nil {
			t.Fatal(err)
		}
		if fail {
			m.lib = nil
		}
		next, cmd := m.Update(modelpicker.SelectedMsg{Model: models["glm53-q2"]})
		m = next.(model)
		if cmd == nil || m.engine != nil || m.session != nil || !m.switchingModel || m.lifecycle.status != engineinit.StatusOpening {
			t.Fatal("switch did not take ownership of old engine")
		}
		if m.history[0].Content != "draw a pelican" || len(m.history) != 2 || m.history[1].ToolCallID != svgCheckpointID || !m.resumeDraft {
			t.Fatal("work was not checkpointed")
		}
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if m.lifecycle.pendingSubmit {
			t.Fatal("submit queued while switching")
		}
		batch := cmd().(tea.BatchMsg)
		m = update(t, m, batch[1]())
		if !m.loading.Active || m.loading.Frame != 1 {
			t.Fatal("switch animation did not start")
		}
		ready := batch[0]().(engineReadyMsg)
		if _, err := oldEng.NewSession(256); err == nil {
			t.Fatal("old engine still open")
		}
		if err := oldSess.Sync([]int{1}); err == nil {
			t.Fatal("old session still open")
		}
		m = update(t, m, ready)
		m = update(t, m, padui.LoadingTick{Epoch: m.loading.Epoch})
		if m.loading.Active || m.loading.Frame != 1 {
			t.Fatal("switch animation continued after engine finished")
		}
		if m.switchingModel || m.generating || m.modelInfo.Alias != "glm53-q2" {
			t.Fatal("incorrect post-switch state")
		}
		if fail {
			if m.lifecycle.status != engineinit.StatusError || m.engine != nil || m.errText == "" {
				t.Fatal("load failure not recoverable")
			}
		} else {
			if m.lifecycle.status != engineinit.StatusReady || m.session == nil || m.session == oldSess {
				t.Fatal("new session not adopted")
			}
			m.session.Close()
			m.engine.Close()
		}
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, []byte(visualFixture)) {
			t.Fatal("draft changed during switch")
		}
	}
}

func TestSwitchBlockedWhileBusyAndPickerDoesNotEditPrompt(t *testing.T) {
	for _, busy := range []string{"generation", "metadata", "opening", "release"} {
		m := testModel()
		switch busy {
		case "generation":
			m.generating = true
		case "metadata":
			m.metadataInFlight = true
		case "opening":
			m.lifecycle.status = engineinit.StatusOpening
		case "release":
			m.releasingEngine = true
		}
		if cmd := m.openModelPicker(); cmd != nil || m.modelPicker.IsOpen() {
			t.Fatalf("opened while %s", busy)
		}
		next, cmd := m.switchModel(ds4.ModelInfo{})
		if cmd != nil || next.(model).switchingModel {
			t.Fatalf("switched while %s", busy)
		}
	}
	m := testModel()
	m.input = textinput.New()
	m.input.SetValue("keep this prompt")
	m.input.Focus()
	m = update(t, m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if !m.modelPicker.IsOpen() {
		t.Fatal("shortcut did not open picker")
	}
	m = update(t, m, tea.KeyPressMsg{Text: "glm"})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.modelPicker.IsOpen() || m.input.Value() != "keep this prompt" || !m.input.Focused() {
		t.Fatal("picker changed prompt/focus")
	}
}

func TestQuitDuringSwitchWaitsForOwnedEngine(t *testing.T) {
	m := testModel()
	m.switchingModel = true
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd != nil || !m.quitAfterSwitch {
		t.Fatal("quit abandoned in-flight engine")
	}
	lib := ds4api.NewMockLibrary()
	result := engineinit.Open(lib, ds4.EngineOptions{}, 256)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	next, cmd = m.Update(engineReadyMsg(result))
	m = next.(model)
	defer m.engine.Close()
	defer m.session.Close()
	if cmd == nil {
		t.Fatal("quit not resumed after open")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("expected quit")
	}
	if m.session != result.Session || !strings.Contains(m.statusText, "Waiting") {
		t.Fatal("engine ownership lost")
	}
}

func TestCurrentModelCanReloadWithVisionEncoder(t *testing.T) {
	models := installedPickerModels(t)
	m := navigationModel()
	m.workDir = t.TempDir()
	m.ctxSize = 8192
	m.lib = ds4api.NewMockLibrary()
	var err error
	m.engine, err = m.lib.NewEngine(ds4.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.engine.Close()
	info := models["glm53-q2"]
	m.modelInfo = &info
	m.modelPath = info.Path
	m.engOpts.ModelPath = info.Path
	m.visual.Mode = "on"
	next, cmd := m.switchModel(info)
	switched := next.(model)
	if !switched.switchingModel || cmd == nil || switched.engOpts.VisionPath != models["glm53-vision"].Path {
		t.Fatal("same-model selection did not prepare encoder reload")
	}
}
