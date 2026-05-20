package editmode

import (
	"reflect"
	"strings"
	"testing"
)

func sampleKeymap() Keymap {
	return Keymap{
		Edit: []Binding{
			{"enter", "send"},
			{"esc", "exit"},
		},
		Command: []Binding{
			{"e", "edit"},
			{"n", "new"},
			{"ctrl+c", "quit"},
		},
	}
}

func TestFooterTextCommandMode(t *testing.T) {
	out := sampleKeymap().FooterText(false)
	for _, want := range []string{"e", "edit", "n", "new", "ctrl+c", "quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("command footer %q missing %q", out, want)
		}
	}
	if strings.Contains(out, "send") {
		t.Errorf("command footer %q leaked edit-mode binding", out)
	}
}

func TestFooterTextEditMode(t *testing.T) {
	out := sampleKeymap().FooterText(true)
	if !strings.Contains(out, "send") || !strings.Contains(out, "exit") {
		t.Errorf("edit footer %q missing edit bindings", out)
	}
	if strings.Contains(out, "new") {
		t.Errorf("edit footer %q leaked command-mode binding", out)
	}
}

func TestFooterTextSeparator(t *testing.T) {
	out := sampleKeymap().FooterText(false)
	if got := strings.Count(out, "·"); got != 2 {
		t.Errorf("footer %q: got %d separators, want 2 for 3 bindings", out, got)
	}
}

func TestFooterTextEmpty(t *testing.T) {
	if out := (Keymap{}).FooterText(false); out != "" {
		t.Errorf("empty keymap footer = %q, want empty string", out)
	}
}

func TestDefaultEditBindings(t *testing.T) {
	got := DefaultEditBindings()
	want := []Binding{
		{"enter", "send"},
		{"esc", "exit"},
		{"ctrl+a/e", "home/end"},
		{"ctrl+w", "del word"},
		{"ctrl+c", "quit"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultEditBindings() =\n  %+v\nwant\n  %+v", got, want)
	}
}

func TestFooterTextKeyOnly(t *testing.T) {
	out := Keymap{Command: []Binding{{Keys: "tab"}}}.FooterText(false)
	if !strings.Contains(out, "tab") {
		t.Errorf("footer %q missing key-only binding", out)
	}
	if strings.Contains(out, "·") {
		t.Errorf("single-binding footer %q should have no separator", out)
	}
}
