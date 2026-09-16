package modelpicker

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/charmbracelet/x/ansi"
)

var (
	pickerHeading  = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	pickerDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	pickerText     = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	pickerWarning  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	pickerError    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	pickerSelected = pickerHeading.Background(lipgloss.Color("236"))
)

// cell fits text by terminal cells, so Unicode aliases and ANSI styling cannot
// shift neighboring columns. Numeric cells are padded on the left.
func cell(text string, width int, right bool) string {
	text = ansi.Truncate(text, max(0, width), "…")
	padding := strings.Repeat(" ", max(0, width-lipgloss.Width(text)))
	if right {
		return padding + text
	}
	return text + padding
}

type pickerColumns struct{ model, size, vision, state int }

func columnsFor(width int) pickerColumns {
	c := pickerColumns{model: max(1, width-2)} // reserve the selection marker
	if width >= 28 {
		c.size = 8
		c.model -= c.size + 2
	}
	if width >= 48 {
		c.vision = 16
		c.model -= c.vision + 2
	}
	if width >= 68 {
		c.state = 9
		c.model -= c.state + 2
	}
	return c
}

func (c pickerColumns) row(marker, name, size, vision, state string) string {
	row := marker + cell(name, c.model, false)
	if c.size > 0 {
		row += "  " + cell(size, c.size, true)
	}
	if c.vision > 0 {
		row += "  " + cell(vision, c.vision, false)
	}
	if c.state > 0 {
		row += "  " + cell(state, c.state, false)
	}
	return row
}

func (m Model) visionLabel(info ds4.ModelInfo) string {
	if !info.Vision {
		return "text"
	}
	for _, companion := range m.models {
		if companion.Alias == info.Encoder && companion.Installed {
			return "vision"
		}
	}
	return "encoder missing"
}

// View renders the same rounded panels, blue headings, muted hints, and active
// row colors as the pad TUIs. Columns collapse on narrow terminals; numeric
// sizes remain right-aligned and the selected row stays in the visible window.
func (m Model) View(width, height int) string {
	width, height = max(1, min(88, width)), max(1, min(24, height))
	if width < 8 || height < 5 {
		return lipgloss.NewStyle().MaxWidth(width).MaxHeight(height).Render(ansi.Truncate("Choose model", width, "…"))
	}
	innerW, innerH := width-4, height-2 // rounded border and one cell of side padding
	fit := func(s string) string { return cell(s, innerW, false) }
	items := m.matches()
	search := m.query
	if search == "" {
		search = "type an alias or family…"
	}
	lines := []string{pickerHeading.Render(fit("Choose model")), pickerDim.Render(fit("Search: " + search))}
	foot := []string{pickerDim.Render(fit("↑/↓ select · Enter switch · Esc cancel"))}
	if m.loading {
		lines = append(lines, pickerDim.Render(fit("Loading installed models…")))
	} else if len(items) == 0 {
		lines = append(lines, pickerText.Render(fit("No matching installed chat models.")), pickerDim.Render(fit("Install with: ds4go model download <alias>")))
	} else {
		cols := columnsFor(innerW)
		lines = append(lines, pickerHeading.Render(cols.row("  ", "Model", "Size", "Vision", "Status")), pickerDim.Render(strings.Repeat("─", innerW)))
		selected := items[m.cursor]
		detail := fmt.Sprintf("%d/%d · %s · %s", m.cursor+1, len(items), selected.Family, m.visionLabel(selected))
		if selected.Path == m.current {
			detail += " · [current]"
		}
		foot = append([]string{pickerDim.Render(fit(detail))}, foot...)
		if m.unavailable != nil {
			if reason := m.unavailable(selected); reason != "" {
				foot = append([]string{pickerWarning.Render(fit(reason))}, foot...)
			}
		}
		rows := max(1, innerH-len(lines)-len(foot)-boolLine(m.err != ""))
		start := max(0, m.cursor-rows+1)
		for i := start; i < min(len(items), start+rows); i++ {
			info := items[i]
			marker, state, size := "  ", "", "—"
			if info.Path == m.current {
				state = "[current]"
			}
			if info.SizeGB > 0 {
				size = fmt.Sprintf("%g GB", info.SizeGB)
			}
			style := pickerText
			if m.unavailable != nil && m.unavailable(info) != "" {
				style = pickerDim
			}
			if i == m.cursor {
				marker, style = "> ", pickerSelected
			}
			lines = append(lines, style.Render(cols.row(marker, info.Alias, size, m.visionLabel(info), state)))
		}
	}
	if m.err != "" {
		foot = append([]string{pickerError.Render(fit(m.err))}, foot...)
	}
	lines = append(lines, foot...)
	if len(lines) > innerH {
		lines = lines[:innerH]
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("75")).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func boolLine(present bool) int {
	if present {
		return 1
	}
	return 0
}
