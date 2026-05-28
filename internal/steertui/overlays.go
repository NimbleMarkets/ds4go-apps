package steertui

import (
	"fmt"
	"sort"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/steerinspect"
)

// RenderHelpOverlay creates a centered, styled modal listing all shortcuts.
func RenderHelpOverlay(width, height int) string {
	boxWidth := 66
	boxHeight := 24

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render("      ds4go-steer TUI Help      ") + "\n\n")
	sb.WriteString(PrimaryStyle.Render("Navigation & Selection:") + "\n")
	sb.WriteString(MutedStyle.Render("  tab       ") + PrimaryStyle.Render("Switch focus between panes") + "\n")
	sb.WriteString(MutedStyle.Render("  j/k, ↓/↑  ") + PrimaryStyle.Render("Move cursor (lanes/alts/steps)") + "\n")
	sb.WriteString(MutedStyle.Render("  [ / ]     ") + PrimaryStyle.Render("Step backward / forward in time") + "\n")
	sb.WriteString(MutedStyle.Render("  shift+↓/↑ ") + PrimaryStyle.Render("Jump 5 steps in transcript") + "\n")
	sb.WriteString(MutedStyle.Render("  enter     ") + PrimaryStyle.Render("Select lane/alt or run continuously") + "\n\n")
	sb.WriteString(PrimaryStyle.Render("Generation Control:") + "\n")
	sb.WriteString(MutedStyle.Render("  space     ") + PrimaryStyle.Render("Generate one token in active lane") + "\n")
	sb.WriteString(MutedStyle.Render("  r         ") + PrimaryStyle.Render("Rewind active lane to selected step") + "\n")
	sb.WriteString(MutedStyle.Render("  b         ") + PrimaryStyle.Render("Branch lane from step using selected alt") + "\n")
	sb.WriteString(MutedStyle.Render("  e         ") + PrimaryStyle.Render("Focus prompt to edit it") + "\n")
	sb.WriteString(MutedStyle.Render("  n         ") + PrimaryStyle.Render("Clear prompt and focus it") + "\n\n")
	sb.WriteString(PrimaryStyle.Render("Dynamic Steering & Session:") + "\n")
	sb.WriteString(MutedStyle.Render("  ctrl+s    ") + PrimaryStyle.Render("Open manual steering override menu") + "\n")
	sb.WriteString(MutedStyle.Render("  c         ") + PrimaryStyle.Render("Open diff picker (or re-enter saved diff)") + "\n")
	sb.WriteString(MutedStyle.Render("  C         ") + PrimaryStyle.Render("Force-open diff picker") + "\n")
	sb.WriteString(MutedStyle.Render("  o         ") + PrimaryStyle.Render("Toggle Engine Logs row below transcript (Tab to focus, ↑/↓ to scroll)") + "\n")
	sb.WriteString(MutedStyle.Render("  v         ") + PrimaryStyle.Render("Open steering vector browser") + "\n")
	sb.WriteString(MutedStyle.Render("  esc, ?    ") + PrimaryStyle.Render("Close overlay / Help") + "\n")
	sb.WriteString(MutedStyle.Render("  q, ctrl+c ") + PrimaryStyle.Render("Quit TUI") + "\n")

	sb.WriteString("\n" + PrimaryStyle.Render("Diff View:") + "\n")
	sb.WriteString(MutedStyle.Render("  j/k, ↓/↑  ") + PrimaryStyle.Render("Scroll one line") + "\n")
	sb.WriteString(MutedStyle.Render("  J / K     ") + PrimaryStyle.Render("Page down / up") + "\n")
	sb.WriteString(MutedStyle.Render("  g / G     ") + PrimaryStyle.Render("Jump to top / bottom") + "\n")
	sb.WriteString(MutedStyle.Render("  f         ") + PrimaryStyle.Render("Jump to fork marker") + "\n")
	sb.WriteString(MutedStyle.Render("  enter     ") + PrimaryStyle.Render("Expand / collapse shared prefix") + "\n")
	sb.WriteString(MutedStyle.Render("  s         ") + PrimaryStyle.Render("Toggle steering-change glyph (⚑)") + "\n")
	sb.WriteString(MutedStyle.Render("  esc       ") + PrimaryStyle.Render("Exit diff view") + "\n")

	content := sb.String()

	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(ColorAccent).
		Padding(1, 2).
		Width(boxWidth).
		Height(boxHeight)

	renderedModal := modalStyle.Render(content)
	return centerOverlay(width, height, renderedModal, boxWidth, boxHeight)
}

// SteeringMenuState manages the interactive manual steering override inputs.
type SteeringMenuState struct {
	Active     bool
	FieldIndex int // 0: Vector, 1: FFN Scale, 2: Attn Scale, 3: Threshold, 4: Mode, 5: Scope, 6: [Apply], 7: [Cancel]

	// Field inputs (as strings for easy typing)
	VectorSelectionIndex int // index into registry vector list
	FFNScaleStr          string
	AttnScaleStr         string
	ThresholdStr         string
	ModeIndex            int // index into allowed modes
	ScopeIndex           int // index into scopes
}

var modes = []steerinspect.SteeringMode{
	steerinspect.SteeringAblation,
	steerinspect.SteeringThreshold,
	steerinspect.SteeringAdditive,
}

var scopes = []steerinspect.SteeringScope{
	steerinspect.SteeringScopeNextMessage,
	steerinspect.SteeringScopeUntilRevert,
	steerinspect.SteeringScopeOff,
}

// RenderSteeringMenu creates the Ctrl-S manual override popup.
func RenderSteeringMenu(width, height int, state *SteeringMenuState, reg *steerinspect.Registry) string {
	boxWidth := 55
	boxHeight := 18

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render("   Manual Steering Configuration Override   ") + "\n\n")

	// Get sorted vector names for selection.
	var sorted []string
	if reg != nil {
		for name := range reg.Vectors {
			sorted = append(sorted, name)
		}
	}
	sort.Strings(sorted)
	vectorNames := append([]string{"[None / Disable]"}, sorted...)

	selectedVectorName := vectorNames[state.VectorSelectionIndex]

	renderField := func(label string, val string, idx int) string {
		marker := "  "
		if state.FieldIndex == idx {
			marker = "▸ "
		}
		labelStyle := PrimaryStyle
		if state.FieldIndex == idx {
			labelStyle = TitleStyle
		}
		return fmt.Sprintf("%s%s %s\n", marker, labelStyle.Render(label), val)
	}

	// 0. Vector selection
	sb.WriteString(renderField("Vector:   ", fmt.Sprintf("< %s >", selectedVectorName), 0))

	// 1. FFN Scale
	sb.WriteString(renderField("FFN Scale:", fmt.Sprintf("[%s]", state.FFNScaleStr), 1))

	// 2. Attn Scale
	sb.WriteString(renderField("Attn Scale:", fmt.Sprintf("[%s]", state.AttnScaleStr), 2))

	// 3. Threshold (CAST)
	sb.WriteString(renderField("Threshold:", fmt.Sprintf("[%s]", state.ThresholdStr), 3))

	// 4. Mode
	currentMode := modes[state.ModeIndex]
	sb.WriteString(renderField("Mode:     ", fmt.Sprintf("< %s >", currentMode.String()), 4))

	// 5. Scope
	currentScope := scopes[state.ScopeIndex]
	sb.WriteString(renderField("Scope:    ", fmt.Sprintf("< %s >", currentScope.String()), 5))

	sb.WriteString("\n")

	// Buttons: [Apply] and [Cancel]
	applyMarker := "  "
	applyStyle := PrimaryStyle
	if state.FieldIndex == 6 {
		applyMarker = "▸ "
		applyStyle = SelectedStyle
	}
	cancelMarker := "  "
	cancelStyle := PrimaryStyle
	if state.FieldIndex == 7 {
		cancelMarker = "▸ "
		cancelStyle = SelectedStyle
	}

	sb.WriteString(fmt.Sprintf("%s %s    %s %s\n\n",
		applyMarker, applyStyle.Render(" [ APPLY ] "),
		cancelMarker, cancelStyle.Render(" [ CANCEL ] ")))

	sb.WriteString(MutedStyle.Render("Use ↓/↑ to navigate fields. Left/Right arrows to cycle options.\nType numbers for scales & thresholds. Press Esc to exit."))

	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(ColorAccent).
		Padding(1, 2).
		Width(boxWidth).
		Height(boxHeight)

	renderedModal := modalStyle.Render(sb.String())
	return centerOverlay(width, height, renderedModal, boxWidth, boxHeight)
}

// RenderPromptInput creates an overlay centered box prompting for initial prompt input.
func RenderPromptInput(width, height int, currentInput string) string {
	boxWidth := 60
	boxHeight := 9

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render("       Enter Initial Prompt       ") + "\n\n")
	sb.WriteString(PrimaryStyle.Render("Type prompt below and press Enter to initialize model:") + "\n\n")
	sb.WriteString(SelectedStyle.Render("> "+currentInput) + "\n\n")
	sb.WriteString(MutedStyle.Render("Press Esc or Ctrl+C to exit."))

	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(ColorAccent).
		Padding(1, 2).
		Width(boxWidth).
		Height(boxHeight)

	renderedModal := modalStyle.Render(sb.String())
	return centerOverlay(width, height, renderedModal, boxWidth, boxHeight)
}

func centerOverlay(width, height int, modal string, boxWidth, boxHeight int) string {
	if width <= boxWidth || height <= boxHeight {
		return modal
	}

	leftPadding := (width - boxWidth) / 2
	topPadding := (height - boxHeight) / 2

	var sb strings.Builder
	for i := 0; i < topPadding; i++ {
		sb.WriteString("\n")
	}

	lines := strings.Split(modal, "\n")
	padStr := strings.Repeat(" ", leftPadding)
	for _, line := range lines {
		sb.WriteString(padStr + line + "\n")
	}

	return sb.String()
}

// RenderVectorBrowser renders a scrollable list of registered steering vectors in a modal overlay.
func RenderVectorBrowser(width, height int, registry *steerinspect.Registry, selectedIdx int) string {
	boxWidth := 74
	boxHeight := 18

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render("             Steering Vector Browser             ") + "\n")
	sb.WriteString(MutedStyle.Render("   Use j/k or arrows to scroll. Enter to select, Esc to close.") + "\n\n")

	// Get sorted names
	var names []string
	if registry != nil {
		for name := range registry.Vectors {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	// Column Headers
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	header := fmt.Sprintf("  %-16s %-16s %-8s %-8s %-12s", "Vector Name", "Dimension File", "Def FFN", "Max FFN", "Callable")
	sb.WriteString(headerStyle.Render(header) + "\n")
	sb.WriteString(MutedStyle.Render("  "+strings.Repeat("─", boxWidth-6)) + "\n")

	// We calculate how many lines we can show for list items.
	// Title/Instructions = 3 lines.
	// Header/Divider = 2 lines.
	// Details divider/Description/Padding = 4 lines.
	// Available height inside modal is boxHeight - 9.
	visibleCount := boxHeight - 9
	if visibleCount < 1 {
		visibleCount = 1
	}

	startIdx := 0
	if len(names) > visibleCount {
		startIdx = selectedIdx - visibleCount/2
		if startIdx < 0 {
			startIdx = 0
		}
		if startIdx+visibleCount > len(names) {
			startIdx = len(names) - visibleCount
		}
	}

	// Render items
	for i := 0; i < visibleCount; i++ {
		idx := startIdx + i
		if idx >= len(names) {
			sb.WriteString("\n")
			continue
		}

		name := names[idx]
		v := registry.Vectors[name]

		// Format columns
		callableStr := "no"
		if v.ModelCallable {
			callableStr = "yes"
		}
		itemStr := fmt.Sprintf("  %-16s %-16s %-8.1f %-8.1f %-12s",
			truncateRunes(name, 16),
			truncateRunes(v.File, 16),
			v.DefaultFFN,
			v.MaxFFN,
			callableStr,
		)

		if idx == selectedIdx {
			sb.WriteString(SelectedStyle.Render(fmt.Sprintf("%-*s", boxWidth-4, itemStr)) + "\n")
		} else {
			sb.WriteString(PrimaryStyle.Render(itemStr) + "\n")
		}
	}

	sb.WriteString(MutedStyle.Render("  "+strings.Repeat("─", boxWidth-6)) + "\n")

	// Render details of selected item
	if selectedIdx >= 0 && selectedIdx < len(names) {
		name := names[selectedIdx]
		v := registry.Vectors[name]
		desc := "Description: " + v.Description
		if len(v.AllowedModes) > 0 {
			desc += fmt.Sprintf(" (Modes: %s)", strings.Join(v.AllowedModes, ", "))
		}
		sb.WriteString(PrimaryStyle.Render("  "+truncateRunes(desc, boxWidth-6)) + "\n")
	} else {
		sb.WriteString("\n")
	}

	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(ColorAccent).
		Padding(1, 2).
		Width(boxWidth).
		Height(boxHeight)

	renderedModal := modalStyle.Render(sb.String())
	return centerOverlay(width, height, renderedModal, boxWidth, boxHeight)
}

// RenderDiffPicker renders an overlay listing eligible lane pairs for diffing.
// selectedIdx is the currently highlighted index within pairs.
func RenderDiffPicker(width, height int, pairs []steerinspect.EligiblePair, selectedIdx int) string {
	if len(pairs) == 0 {
		body := MutedStyle.Render("No eligible lane pairs (need at least two lanes).")
		return centerOverlay(width, height, body, 50, 3)
	}

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render("Pick lanes to diff") + "\n\n")
	for i, p := range pairs {
		row := fmt.Sprintf("%s ↔ %s   fork@%d", p.LeftLabel, p.RightLabel, p.ForkStep)
		if i == selectedIdx {
			sb.WriteString(SelectedStyle.Render("▸ "+row) + "\n")
		} else {
			sb.WriteString(PrimaryStyle.Render("  "+row) + "\n")
		}
	}
	sb.WriteString("\n" + MutedStyle.Render("↑/↓ select · enter confirm · esc cancel"))
	body := sb.String()

	boxW := 60
	if boxW > width-4 {
		boxW = width - 4
	}
	boxH := len(pairs) + 5
	if boxH > height-4 {
		boxH = height - 4
	}
	return centerOverlay(width, height, body, boxW, boxH)
}
