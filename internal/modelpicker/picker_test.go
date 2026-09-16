package modelpicker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
)

func fixturePicker() Model {
	return Model{open: true, request: 1, current: "/b", models: []ds4.ModelInfo{
		{Alias: "alpha", Path: "/a", Family: "deepseek", Installed: true},
		{Alias: "beta", Path: "/b", Family: "glm", Installed: true, Vision: true, Encoder: "encoder"},
		{Alias: "encoder", Installed: true, Optional: true},
		{Alias: "shard", Installed: true, Distributed: true},
		{Alias: "absent", Installed: false},
	}}
}

func TestFilterAndSelectInstalledChatModels(t *testing.T) {
	m := fixturePicker()
	if len(m.matches()) != 2 {
		t.Fatal("companions or uninstalled models offered")
	}
	m, _ = m.Update(tea.KeyPressMsg{Text: "glm"})
	if len(m.matches()) != 1 || m.matches()[0].Alias != "beta" {
		t.Fatal("family search failed")
	}
	view := m.View(88, 20)
	if !strings.Contains(view, "[current]") || !strings.Contains(view, "vision") || strings.Contains(view, "encoder missing") {
		t.Fatalf("view=%s", view)
	}
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.IsOpen() || cmd == nil || cmd().(SelectedMsg).Model.Alias != "beta" {
		t.Fatal("selection failed")
	}
}

func TestPickerCancelDisabledEmptyAndStaleLoad(t *testing.T) {
	m := fixturePicker()
	m.unavailable = func(ds4.ModelInfo) string { return "vision required" }
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || !m.IsOpen() || m.err != "vision required" {
		t.Fatal("disabled model selected")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m, _ = m.Update(LoadedMsg{request: 1, models: []ds4.ModelInfo{{Alias: "late"}}})
	if m.IsOpen() {
		t.Fatal("late load reopened picker")
	}
	m = fixturePicker()
	m, _ = m.Update(LoadedMsg{request: 0, err: fmt.Errorf("stale error")})
	if m.err != "" {
		t.Fatal("stale load applied")
	}
	m, _ = m.Update(tea.KeyPressMsg{Text: "no match"})
	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || !strings.Contains(m.View(88, 20), "No matching") {
		t.Fatal("empty search selected a model")
	}
}

func TestPickerScrollingAndSmallTerminals(t *testing.T) {
	m := fixturePicker()
	for i := 0; i < 30; i++ {
		m.models = append(m.models, ds4.ModelInfo{Alias: fmt.Sprintf("model%d", i), Installed: true})
	}
	for i := 0; i < 40; i++ {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.cursor != len(m.matches())-1 {
		t.Fatal("cursor escaped list")
	}
	if !strings.Contains(m.View(60, 12), "> model29") {
		t.Fatal("selected row not visible")
	}
	for _, dims := range [][2]int{{1, 1}, {12, 4}, {60, 12}} {
		view := m.View(dims[0], dims[1])
		if lipgloss.Width(view) > dims[0] || lipgloss.Height(view) > dims[1] {
			t.Fatalf("unbounded view: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
		}
	}
}

func TestOpenLoadsFreshCatalogAndNormalizesCurrentModel(t *testing.T) {
	t.Setenv("DS4_DIR", t.TempDir())
	if err := os.MkdirAll(ds4.DefaultModelsDir(), 0755); err != nil {
		t.Fatal(err)
	}
	list, err := ds4.ListModels()
	if err != nil {
		t.Fatal(err)
	}
	var info ds4.ModelInfo
	for _, item := range list {
		if item.Alias == "q2-imatrix" {
			info = item
			break
		}
	}
	if info.Path == "" {
		t.Fatal("missing fixture alias")
	}
	var m Model
	load := m.Open("", nil)
	m, _ = m.Update(load())
	if len(m.matches()) != 0 {
		t.Fatal("uninstalled models listed")
	}
	if err := os.WriteFile(info.Path, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(info.Path, ds4.DefaultModelPath()); err != nil {
		t.Fatal(err)
	}
	load = m.Open(ds4.DefaultModelPath(), nil)
	m, _ = m.Update(load())
	if len(m.matches()) != 1 || m.current != info.Path || !strings.Contains(m.View(88, 20), "[current]") {
		t.Fatal("catalog refresh/default identity failed")
	}
	custom := filepath.Join(t.TempDir(), "custom.gguf")
	if err := os.WriteFile(custom, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	load = m.Open(custom, nil)
	m, _ = m.Update(load())
	if len(m.matches()) != 2 || m.matches()[m.cursor].Path != custom {
		t.Fatal("current custom model omitted")
	}
}
