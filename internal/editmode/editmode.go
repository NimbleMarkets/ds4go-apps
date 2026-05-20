// Package editmode provides the shared modal edit-box keymap and help
// rendering used by the glyphpad and svgpad TUIs. The edit box is in one
// of two modes — targeted (typing) or not (bare-letter commands) — and the
// footer help strip swaps to match.
package editmode

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Help-strip styling. The caller wraps FooterText's result in its own
// footer background; this package owns only the per-binding treatment so
// glyphpad and svgpad render help identically.
var (
	keyStyle  = lipgloss.NewStyle().Bold(true)
	descStyle = lipgloss.NewStyle().Faint(true)
	separator = " · "
)

// Binding is one key-to-action mapping shown in a help strip.
type Binding struct {
	Keys string // key chord, e.g. "e", "enter", "ctrl+c"
	Desc string // short action label, e.g. "edit"
}

// Keymap holds the bindings shown in each mode of the modal edit box.
type Keymap struct {
	Edit    []Binding // shown while the edit box is targeted
	Command []Binding // shown while it is not
}

// DefaultEditBindings is the help strip shown while the edit box is
// targeted: send/exit plus the textinput's most useful built-in editor
// keys (line start/end, delete-word), then quit. Both apps use this set
// as-is since the bubbles textinput honors the same chords in each.
func DefaultEditBindings() []Binding {
	return []Binding{
		{"enter", "send"},
		{"esc", "exit"},
		{"ctrl+a/e", "home/end"},
		{"ctrl+w", "del word"},
		{"ctrl+c", "quit"},
	}
}

// FooterText renders the help strip for the current mode. A targeted edit
// box selects the Edit bindings; otherwise the Command bindings are shown.
// Each binding's key is bold and its description dimmed, joined by " · ".
func (k Keymap) FooterText(targeted bool) string {
	bindings := k.Command
	if targeted {
		bindings = k.Edit
	}
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		s := keyStyle.Render(b.Keys)
		if b.Desc != "" {
			s += " " + descStyle.Render(b.Desc)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, separator)
}
