package main

import (
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/spf13/pflag"
)

func TestVisualReviewFlagAlias(t *testing.T) {
	for _, name := range []string{"visual-review", "vision-review"} {
		var visual visualOptions
		fs := pflag.NewFlagSet("svgpad", pflag.ContinueOnError)
		registerVisualFlags(fs, &visual)
		if err := fs.Parse([]string{"--" + name, "on"}); err != nil {
			t.Fatal(err)
		}
		if visual.Mode != "on" || visual.MaxPasses != 3 {
			t.Fatalf("flags: %+v", visual)
		}
	}
}

func TestStartupPickerRequirement(t *testing.T) {
	for _, tc := range []struct {
		name, mode, model, encoder string
		info                       *ds4.ModelInfo
		want                       bool
	}{
		{name: "missing model", mode: "on", want: true},
		{name: "text model", mode: "on", model: "text.gguf", info: &ds4.ModelInfo{}, want: true},
		{name: "text with encoder", mode: "on", model: "text.gguf", encoder: "vision.gguf", info: &ds4.ModelInfo{}, want: true},
		{name: "encoder missing", mode: "on", model: "vision.gguf", info: &ds4.ModelInfo{Vision: true}, want: true},
		{name: "vision ready", mode: "on", model: "vision.gguf", encoder: "encoder.gguf", info: &ds4.ModelInfo{Vision: true}},
		{name: "custom pair", mode: "on", model: "custom.gguf", encoder: "custom-encoder.gguf"},
		{name: "auto text", mode: "auto", model: "text.gguf"},
		{name: "off text", mode: "off", model: "text.gguf"},
	} {
		m := model{modelPath: tc.model, modelInfo: tc.info, engOpts: ds4.EngineOptions{VisionPath: tc.encoder}, visual: visualOptions{Mode: tc.mode}}
		if got := m.needsVisionModelSelection(); got != tc.want {
			t.Errorf("%s: need picker=%v", tc.name, got)
		}
	}
}

func TestStartupPickerCancelAndReselect(t *testing.T) {
	models := installedPickerModels(t)
	m := testModel()
	m.input = textinput.New()
	m.input.SetValue("keep my prompt")
	m.input.Focus()
	m.modelPath = models["q2-imatrix"].Path
	info := models["q2-imatrix"]
	m.modelInfo = &info
	m.lifecycle.status = engineinit.StatusDormant
	m.visual = visualOptions{Mode: "on", MaxPasses: 3}
	batch := m.Init()().(tea.BatchMsg)
	startup, ok := batch[0]().(startupModelSelectionMsg)
	if !ok {
		t.Fatal("startup did not request picker")
	}
	next, load := m.Update(startup)
	m = next.(model)
	if !m.modelPicker.IsOpen() || load == nil || m.lifecycle.status != engineinit.StatusDormant {
		t.Fatal("startup tried opening the engine")
	}
	m = update(t, m, load())
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.modelPicker.IsOpen() {
		t.Fatal("text model selectable with vision required")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.modelPicker.IsOpen() || m.input.Value() != "keep my prompt" {
		t.Fatal("cancel lost prompt")
	}
	next, load = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if !m.modelPicker.IsOpen() || load == nil || m.lifecycle.pendingSubmit {
		t.Fatal("submit bypassed required picker")
	}
	m = update(t, m, load())
	m = update(t, m, tea.KeyPressMsg{Text: "glm53-q2"})
	next, selected := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if selected == nil {
		t.Fatal("installed vision model not selectable")
	}
	m.workDir = t.TempDir()
	next, open := m.Update(selected())
	m = next.(model)
	if open == nil || !m.switchingModel || m.needsVisionModelSelection() || m.engOpts.VisionPath != models["glm53-vision"].Path {
		t.Fatal("selection did not configure matching encoder")
	}
	// No native model load is needed: the nil-library command reports its error.
	m = update(t, m, open().(tea.BatchMsg)[0]())
	if m.switchingModel {
		t.Fatal("failed load left picker inaccessible")
	}
}
