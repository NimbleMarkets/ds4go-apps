// Package padui contains focus-preserving workspace dialogs shared by the pads.
// Hosts supply available actions and settings; this package owns navigation
// and appearance, never an engine, document, or prompt editor.
package padui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/editmode"
	"github.com/charmbracelet/x/ansi"
)

type Row struct{ ID, Label, Value, Disabled string }
type Change struct {
	ID    string
	Delta int
}
type Model struct {
	Kind             string
	Selected, Scroll int
	Notice           string
}

// Globals is shared by routing, help, and footer. Busy applies to model changes;
// reasoning is independently frozen by the host while an engine is loading.
func Globals(busy bool) editmode.Actions {
	blocked := ""
	if busy {
		blocked = "Wait for the current work to finish"
	}
	return editmode.Actions{
		{ID: "help", Keys: []string{"f1"}, Label: "help", Group: "Workspace"},
		{ID: "settings", Keys: []string{"f2"}, Label: "settings", Group: "Workspace"},
		{ID: "reason", Keys: []string{"ctrl+r"}, Label: "reasoning", Group: "Run"},
		{ID: "model", Keys: []string{"ctrl+o"}, Label: "model", Group: "Run", Disabled: blocked},
		{ID: "copy", Keys: []string{"ctrl+y"}, Label: "copy pane", Group: "Workspace"},
		{ID: "logs", Keys: []string{"ctrl+n"}, Label: "logs", Group: "Workspace"},
	}
}

func (m *Model) Open(kind string) {
	if m.Kind == kind {
		m.Kind = ""
	} else {
		m.Kind = kind
	}
	m.Scroll = 0
	m.Selected = 0
	m.Notice = ""
}
func (m *Model) Key(k string, rows []Row) *Change {
	switch k {
	case "esc":
		m.Kind = ""
		m.Notice = ""
	case "up", "shift+tab":
		if m.Kind == "settings" {
			m.Selected = max(0, m.Selected-1)
		} else {
			m.Scroll = max(0, m.Scroll-1)
		}
	case "down", "tab":
		if m.Kind == "settings" {
			m.Selected = min(max(0, len(rows)-1), m.Selected+1)
		} else {
			m.Scroll++
		}
	case "pgup":
		m.Scroll = max(0, m.Scroll-8)
	case "pgdown":
		m.Scroll += 8
	case "left", "right", "enter":
		if m.Kind == "settings" && len(rows) > 0 {
			row := rows[min(m.Selected, len(rows)-1)]
			if row.Disabled != "" {
				m.Notice = row.Disabled
				return nil
			}
			d := 1
			if k == "left" {
				d = -1
			}
			return &Change{ID: row.ID, Delta: d}
		}
	}
	return nil
}

func (m Model) Lines(rows []Row, actions editmode.Actions) []string {
	var lines []string
	if m.Kind == "settings" {
		lines = append(lines, "Settings apply to the next request; changes are not saved.", "")
		for i, r := range rows {
			marker := "  "
			if i == m.Selected {
				marker = "> "
			}
			text := fmt.Sprintf("%s%-18s %s", marker, r.Label, r.Value)
			if r.Disabled != "" {
				text += " (locked)"
			}
			if i == m.Selected {
				text = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Background(lipgloss.Color("236")).Render(text)
			}
			lines = append(lines, text)
		}
	} else {
		for _, a := range actions {
			row := fmt.Sprintf("%-18s %s", strings.Join(a.Keys, "/"), a.Label)
			if a.Disabled != "" {
				row += " · " + a.Disabled
			}
			lines = append(lines, row)
		}
	}
	if m.Notice != "" {
		lines = append(lines, "", m.Notice)
	}
	return lines
}

func (m Model) View(width, height int, rows []Row, actions editmode.Actions) string {
	hint := "↑/↓ · PgUp/PgDown scroll · Esc close"
	if m.Kind == "settings" {
		hint = "↑/↓ select · ←/→ change · Esc close"
	}
	top := m.Scroll
	if m.Kind == "settings" {
		top = max(0, m.Selected+3-max(1, height-6))
	}
	return Dialog(width, height, "Run "+m.Kind, m.Lines(rows, actions), top, hint)
}

func Dialog(width, height int, title string, lines []string, top int, hint string) string {
	w, h := max(1, min(88, width-4)), max(1, height-6)
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(line, w, ""), "\n")...)
	}
	top = min(max(0, top), max(0, len(wrapped)-h))
	body := lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true).Render(title) + "\n" + strings.Join(wrapped[top:min(len(wrapped), top+h)], "\n") + "\n" + ansi.Truncate(hint, w, "…")
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("75")).Padding(0, 1).Width(w + 2).Render(body)
	return lipgloss.Place(max(1, width), max(1, height), lipgloss.Center, lipgloss.Center, box)
}

func Copy(label, text string) (string, tea.Cmd) {
	text = ansi.Strip(text)
	if strings.TrimSpace(text) == "" {
		return "Nothing to copy from " + label, nil
	}
	return "Sent " + label + " to terminal clipboard", tea.SetClipboard(text)
}

func Footer(width int, label string, bindings []editmode.Binding) string {
	prefix := " " + label + " │ "
	text := prefix
	for i := range bindings {
		next := prefix + (editmode.Keymap{Command: bindings[:i+1]}).FooterText(false)
		if lipgloss.Width(next) > width {
			break
		}
		text = next
	}
	return ansi.Truncate(text, max(0, width), "…")
}

// Clamp keeps one upward keystroke effective even after repeatedly paging past
// the end of a short help document.
func (m *Model) Clamp(width, height int, rows []Row, actions editmode.Actions) {
	count := 0
	for _, line := range m.Lines(rows, actions) {
		count += strings.Count(ansi.Wrap(line, max(1, min(88, width-4)), ""), "\n") + 1
	}
	m.Scroll = min(m.Scroll, max(0, count-max(1, height-6)))
}
