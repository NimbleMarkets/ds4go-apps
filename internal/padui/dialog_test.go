package padui

import (
	"charm.land/lipgloss/v2"
	"strings"
	"testing"
)

func TestLockedSettingsAndEscape(t *testing.T) {
	var m Model
	m.Open("settings")
	rows := []Row{{ID: "reason", Disabled: "loading"}, {ID: "temp", Value: "0.7"}}
	if m.Key("right", rows) != nil || m.Notice != "loading" {
		t.Fatal("locked setting changed")
	}
	m.Key("down", rows)
	change := m.Key("left", rows)
	if change == nil || change.ID != "temp" || change.Delta != -1 {
		t.Fatal("wrong setting selected")
	}
	m.Key("x", rows)
	if m.Kind == "" {
		t.Fatal("ordinary key dismissed modal")
	}
	m.Key("esc", rows)
	if m.Kind != "" {
		t.Fatal("escape did not close")
	}
}
func TestGlobalsAvailabilityAndDialogDimensions(t *testing.T) {
	for _, busy := range []bool{false, true} {
		a, ok := Globals(busy).Match("ctrl+o")
		if !ok || (a.Disabled != "") != busy {
			t.Fatal("model availability mismatch")
		}
		for _, k := range []string{"f1", "f2", "ctrl+r", "ctrl+y", "ctrl+n"} {
			a, ok = Globals(busy).Match(k)
			if !ok || a.Disabled != "" {
				t.Fatal("missing global", k)
			}
		}
	}
	for _, size := range [][2]int{{40, 15}, {80, 24}, {120, 40}} {
		view := Dialog(size[0], size[1], "Help", []string{strings.Repeat("long text ", 100)}, 4, "Esc close")
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatal("dialog overflow")
		}
	}
}
