package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/modelpicker"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go/ds4api"
)

func pickerModels(t *testing.T) map[string]ds4.ModelInfo {
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
		case "glm53-q2", "glm53-vision", "q2-imatrix", "qwen38-q2", "qwen38-vision":
			if err := os.WriteFile(info.Path, []byte("fixture"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	list, err = ds4.ListModels()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ds4.ModelInfo{}
	for _, info := range list {
		out[info.Alias] = info
	}
	return out
}
func pickerModel(t *testing.T, info ds4.ModelInfo) *model {
	t.Helper()
	m := testModel(t)
	m.app = &appinit.App{Lib: ds4api.NewMockLibrary(), Flags: &appinit.Flags{Ctx: 256, MTP: "none"}, EngineOpts: ds4.EngineOptions{ModelPath: info.Path, Backend: ds4.BackendCPU, PowerPercent: 65, SSDStreaming: true}, ModelInfo: &info}
	m.width, m.height = 100, 30
	return m
}
func finishLoad(t *testing.T, m *model, cmd tea.Cmd) engineMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no load command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatal("expected engine wait and shared loading ticker")
	}
	msg, ok := batch[0]().(engineMsg)
	if !ok {
		t.Fatal("expected engine result")
	}
	m.Update(msg)
	return msg
}
func loadFixture(t *testing.T, m *model, info ds4.ModelInfo) {
	t.Helper()
	finishLoad(t, m, m.switchModel(info))
	if m.engine == nil {
		t.Fatal(m.status)
	}
}

func TestStartupPickerBeforeLoadingAndExplicitModel(t *testing.T) {
	models := pickerModels(t)
	info := models["q2-imatrix"]
	m := pickerModel(t, info)
	_, cmd := m.Update(startupMsg{})
	if cmd == nil || !m.picker.IsOpen() || m.loader != nil || m.engine != nil {
		t.Fatal("default startup eagerly loaded model")
	}
	m.Update(cmd())
	if !strings.Contains(m.View().Content, "Choose model") {
		t.Fatal("missing shared picker")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.picker.IsOpen() || m.loader != nil {
		t.Fatal("cancel should leave shaders usable without loading")
	}
	m.app.Flags.Model = info.Alias
	_, cmd = m.Update(startupMsg{})
	finishLoad(t, m, cmd)
	if m.engineStatus != engineinit.StatusReady || m.modelDisplayName() != info.Alias {
		t.Fatal("explicit model wasn't adopted")
	}
}

func TestSwitchClosesOldEngineAndPreservesWorkspace(t *testing.T) {
	models := pickerModels(t)
	m := pickerModel(t, models["q2-imatrix"])
	loadFixture(t, m, models["q2-imatrix"])
	oldEngine, oldSession := m.engine, m.session
	m.state.Set("warp", 1.5)
	before := m.state.Snapshot()
	m.input.Focus()
	m.input.SetValue("keep my prompt")
	m.input.SetCursor(4)
	m.history = []ds4.ChatMessage{{Role: "user", Content: "a melting rainbow"}, {Role: "assistant", Content: "old syntax", Parts: []ds4.ContentPart{{Image: &ds4.ImageInput{Data: []byte("old preview")}}}}, {Role: "user", ToolCallID: "pad.tool-budget", Content: "no tools left"}}
	_, cmd := m.Update(modelpicker.SelectedMsg{Model: models["glm53-q2"]})
	if m.engine != nil || m.session != nil || m.loader == nil || !m.loading.Active {
		t.Fatal("switch didn't take ownership")
	}
	if !strings.Contains(m.modelBar(), "glm53-q2") {
		t.Fatal("loading target not visible")
	}
	finishLoad(t, m, cmd)
	if _, err := oldEngine.NewSession(32); err == nil {
		t.Fatal("old engine remains open")
	}
	if err := oldSession.Sync([]int{1}); err == nil {
		t.Fatal("old session remains open")
	}
	if m.engine == nil || m.session == oldSession || m.loading.Active || m.engineStatus != engineinit.StatusReady {
		t.Fatal("replacement not ready")
	}
	if !reflect.DeepEqual(before, m.state.Snapshot()) {
		t.Fatal("shader workspace changed")
	}
	if m.input.Value() != "keep my prompt" || !m.input.Focused() || m.input.Position() != 4 {
		t.Fatal("switch lost prompt/focus")
	}
	if len(m.history) != 1 || !strings.Contains(m.history[0].Content, "a melting rainbow") || len(m.history[0].Parts) != 0 {
		t.Fatal("history not checkpointed")
	}
	if m.engOpts.VisionPath != models["glm53-vision"].Path || m.engOpts.MTPPath != "" || !m.engOpts.SSDStreaming || m.engOpts.PowerPercent != 65 {
		t.Fatal("incorrect runtime settings or companions")
	}
	// An already loaded text model is a no-op, without releasing its engine.
	loadFixture(t, m, models["q2-imatrix"])
	engine := m.engine
	if cmd := m.switchModel(models["q2-imatrix"]); cmd != nil || m.engine != engine || !strings.Contains(m.status, "Already using") {
		t.Fatal("same-model selection reloaded")
	}
}

func TestPickerShortcutWhilePromptFocusedAndCurrentMarker(t *testing.T) {
	models := pickerModels(t)
	m := pickerModel(t, models["q2-imatrix"])
	loadFixture(t, m, models["q2-imatrix"])
	m.input.Focus()
	m.input.SetValue("existing prompt")
	m.input.SetCursor(3)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if cmd == nil || !m.picker.IsOpen() {
		t.Fatal("Ctrl+O failed with loaded engine")
	}
	m.Update(cmd())
	view := m.View().Content
	if !strings.Contains(view, "[current]") || !strings.Contains(view, "q2-imatrix") {
		t.Fatal("picker doesn't identify current model")
	}
	m.Update(tea.KeyPressMsg{Text: "glm", Code: 'g'})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.picker.IsOpen() || m.input.Value() != "existing prompt" || m.input.Position() != 3 || !m.input.Focused() {
		t.Fatal("picker changed prompt")
	}
	if m.slash("/model") == nil || !m.picker.IsOpen() {
		t.Fatal("slash command didn't reopen picker")
	}
}

func TestBusyGuardsAndQuitDuringLoad(t *testing.T) {
	models := pickerModels(t)
	m := pickerModel(t, models["q2-imatrix"])
	m.gen, _ = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) { defer close(ch); <-ctx.Done() })
	if m.openModelPicker() != nil || m.switchModel(models["glm53-q2"]) != nil {
		t.Fatal("switch allowed during generation")
	}
	m.gen.StopAndWait()
	m.gen = nil
	cmd := m.switchModel(models["q2-imatrix"])
	epoch := m.loading.Epoch
	if m.openModelPicker() != nil || m.switchModel(models["glm53-q2"]) != nil {
		t.Fatal("switch allowed during load")
	}
	_, quit := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if quit != nil || !m.quitPending {
		t.Fatal("quit abandoned loader")
	}
	batch := cmd().(tea.BatchMsg)
	ready := batch[0]().(engineMsg)
	_, quit = m.Update(ready)
	if quit == nil {
		t.Fatal("quit not resumed")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("not a quit")
	}
	m.Update(padui.LoadingTick{Epoch: epoch})
	if m.loading.Active {
		t.Fatal("loading restarted after completion")
	}
}

func TestFailedLoadKeepsWorkspaceAndAllowsRetry(t *testing.T) {
	models := pickerModels(t)
	m := pickerModel(t, models["q2-imatrix"])
	loadFixture(t, m, models["q2-imatrix"])
	before := m.state.Snapshot()
	old := m.engine
	// Exercise engineinit failure after ownership transfers, without a real model.
	lib := m.app.Lib
	m.app.Lib = nil
	finishLoad(t, m, m.openEngine(m.engOpts))
	if m.engine != nil || m.loader != nil || m.engineStatus != engineinit.StatusError || !strings.Contains(m.modelBar(), "load failed") {
		t.Fatal("incorrect failure state")
	}
	if _, err := old.NewSession(32); err == nil {
		t.Fatal("failure left previous engine open")
	}
	if !reflect.DeepEqual(before, m.state.Snapshot()) {
		t.Fatal("failed load changed shader")
	}
	m.app.Lib = lib
	if m.openModelPicker() == nil {
		t.Fatal("can't retry after failure")
	}
}

func TestModelBarShowsAliasCapabilityAndFits(t *testing.T) {
	models := pickerModels(t)
	m := pickerModel(t, models["q2-imatrix"])
	loadFixture(t, m, models["q2-imatrix"])
	for _, width := range []int{48, 80, 140} {
		m.width = width
		bar := m.modelBar()
		if !strings.Contains(bar, "q2-imatrix") || !strings.Contains(bar, "text") || lipgloss.Width(bar) > width {
			t.Fatalf("bad model bar at %d: %q", width, bar)
		}
	}
	m.modelInfo = nil
	m.modelPath = filepath.Join("/tmp", "custom.gguf")
	if m.modelDisplayName() != "custom.gguf" {
		t.Fatal("no custom filename")
	}
	m.noEngine = true
	if !strings.Contains(m.modelBar(), "--no-engine") {
		t.Fatal("missing disabled state")
	}
}

func TestCompanionsAndStaleCatalogValidation(t *testing.T) {
	models := pickerModels(t)
	base := ds4.EngineOptions{ModelPath: "old", VisionPath: "old-vision", MTPPath: "old-mtp", Backend: ds4.BackendMetal, SSDStreaming: true, PowerPercent: 70}
	next, err := switchedModelOptions(base, models["glm53-q2"], false)
	if err != nil || next.VisionPath != models["glm53-vision"].Path || next.MTPPath != "" || next.Backend != base.Backend || !next.SSDStreaming {
		t.Fatalf("wrong options: %+v %v", next, err)
	}
	if err := os.WriteFile(models["glm53-q2"].Path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := switchedModelOptions(base, models["glm53-q2"], false); err == nil {
		t.Fatal("accepted missing model")
	}
	if _, err := switchedModelOptions(base, models["glm53-vision"], false); err == nil {
		t.Fatal("accepted encoder as chat model")
	}
}
