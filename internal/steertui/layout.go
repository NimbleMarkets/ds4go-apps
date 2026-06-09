package steertui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/76creates/stickers/flexbox"
)

// FocusArea represents the currently focused pane in the UI.
type FocusArea int

const (
	FocusLanes FocusArea = iota
	FocusTranscript
	FocusAlternatives
	FocusMetrics
	FocusLogs
	FocusPrompt
)

// ViewMode selects the top-level rendering mode of the TUI.
type ViewMode int

const (
	ViewNormal ViewMode = iota
	ViewDiff
)

// Theme colors matching ds4go's palette
var (
	ColorAccent  = lipgloss.Color("#5FBE9E")
	ColorPrimary = lipgloss.Color("#C9D1D9")
	ColorMuted   = lipgloss.Color("#8a929b")
	ColorDark    = lipgloss.Color("#0B1411")
	ColorSurface = lipgloss.Color("#30363D")
	ColorBorder  = lipgloss.Color("#586069")
	ColorActive  = lipgloss.Color("#5FBE9E")
	ColorSteer   = lipgloss.Color("#FFB86C")

	TitleStyle     = lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	MutedStyle     = lipgloss.NewStyle().Foreground(ColorMuted)
	PrimaryStyle   = lipgloss.NewStyle().Foreground(ColorPrimary)
	SelectedStyle  = lipgloss.NewStyle().Bold(true).Foreground(ColorDark).Background(ColorAccent)
	SteerHighlight = lipgloss.NewStyle().Foreground(ColorSteer).Bold(true)

	BorderNormal = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorBorder)
	BorderFocus  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorActive)
)

// GetLayoutDimensions computes the exact dimensions of each pane in the layout.
func GetLayoutDimensions(width, height int, statusView string, showLogs bool) (wTranscript, hTranscript, wAlts, hAlts, wLanes, hLanes, wMetrics, hMetrics, wLogs, hLogs int) {
	statusHeight := strings.Count(statusView, "\n") + 1
	promptHeight := height * 15 / 100
	if promptHeight < 5 {
		promptHeight = 5
	}
	gridHeight := height - 1 - statusHeight - promptHeight
	if gridHeight < 5 {
		gridHeight = 5
	}

	var hTranscriptVal, hLogsVal, hBottomVal int

	if showLogs {
		// Calculate bottom height with a minimum of 14, cap if gridHeight is small
		hBottomVal = gridHeight * 45 / 100
		if hBottomVal < 14 {
			hBottomVal = 14
		}
		if hBottomVal > gridHeight-6 {
			hBottomVal = gridHeight - 6
			if hBottomVal < 1 {
				hBottomVal = 1
			}
		}

		remainingHeight := gridHeight - hBottomVal
		hLogsVal = remainingHeight * 36 / 100
		if hLogsVal < 2 {
			hLogsVal = 2
		}
		hTranscriptVal = remainingHeight - hLogsVal
		if hTranscriptVal < 2 {
			hTranscriptVal = 2
		}
	} else {
		// Calculate bottom height with a minimum of 14, cap if gridHeight is small
		hBottomVal = gridHeight * 60 / 100
		if hBottomVal < 14 {
			hBottomVal = 14
		}
		if hBottomVal > gridHeight-4 {
			hBottomVal = gridHeight - 4
			if hBottomVal < 1 {
				hBottomVal = 1
			}
		}
		hTranscriptVal = gridHeight - hBottomVal
		if hTranscriptVal < 2 {
			hTranscriptVal = 2
		}
		hLogsVal = 0
	}

	flex := flexbox.New(width, gridHeight)

	if showLogs {
		row1 := flex.NewRow()
		cellTranscript := flexbox.NewCell(100, hTranscriptVal).SetID("transcript")
		row1.AddCells(cellTranscript)

		rowLogs := flex.NewRow()
		cellLogs := flexbox.NewCell(100, hLogsVal).SetID("logs")
		rowLogs.AddCells(cellLogs)

		row2 := flex.NewRow()
		cellAlts := flexbox.NewCell(50, hBottomVal).SetID("alternatives")
		cellLanes := flexbox.NewCell(15, hBottomVal).SetID("lanes")
		cellMetrics := flexbox.NewCell(35, hBottomVal).SetID("metrics")
		row2.AddCells(cellAlts, cellLanes, cellMetrics)

		flex.AddRows([]*flexbox.Row{row1, rowLogs, row2})

		cellTranscript.SetContent("")
		cellLogs.SetContent("")
		cellAlts.SetContent("")
		cellLanes.SetContent("")
		cellMetrics.SetContent("")

		_ = flex.Render()

		return cellTranscript.GetWidth(), cellTranscript.GetHeight(),
			cellAlts.GetWidth(), cellAlts.GetHeight(),
			cellLanes.GetWidth(), cellLanes.GetHeight(),
			cellMetrics.GetWidth(), cellMetrics.GetHeight(),
			cellLogs.GetWidth(), cellLogs.GetHeight()
	}

	// 2-row layout.
	row1 := flex.NewRow()
	cellTranscript := flexbox.NewCell(100, hTranscriptVal).SetID("transcript")
	row1.AddCells(cellTranscript)

	row2 := flex.NewRow()
	cellAlts := flexbox.NewCell(50, hBottomVal).SetID("alternatives")
	cellLanes := flexbox.NewCell(15, hBottomVal).SetID("lanes")
	cellMetrics := flexbox.NewCell(35, hBottomVal).SetID("metrics")
	row2.AddCells(cellAlts, cellLanes, cellMetrics)

	flex.AddRows([]*flexbox.Row{row1, row2})

	cellTranscript.SetContent("")
	cellAlts.SetContent("")
	cellLanes.SetContent("")
	cellMetrics.SetContent("")

	_ = flex.Render()

	return cellTranscript.GetWidth(), cellTranscript.GetHeight(),
		cellAlts.GetWidth(), cellAlts.GetHeight(),
		cellLanes.GetWidth(), cellLanes.GetHeight(),
		cellMetrics.GetWidth(), cellMetrics.GetHeight(),
		0, 0
}

// RenderLayout renders the responsive multi-pane layout using stickers/flexbox.
func RenderLayout(width, height int, focus FocusArea, lanesView, transcriptView, alternativesView, metricsView, logsView, promptBox, statusView string, showLogs bool) string {
	statusHeight := strings.Count(statusView, "\n") + 1
	promptHeight := height * 15 / 100
	if promptHeight < 5 {
		promptHeight = 5
	}
	gridHeight := height - 1 - statusHeight - promptHeight
	if gridHeight < 5 {
		gridHeight = 5
	}

	styleLanes := BorderNormal
	styleTranscript := BorderNormal
	styleAlts := BorderNormal
	styleMetrics := BorderNormal
	styleLogs := BorderNormal

	switch focus {
	case FocusLanes:
		styleLanes = BorderFocus
	case FocusTranscript:
		styleTranscript = BorderFocus
	case FocusAlternatives:
		styleAlts = BorderFocus
	case FocusMetrics:
		styleMetrics = BorderFocus
	case FocusLogs:
		styleLogs = BorderFocus
	}

	_, hTranscript, _, hAlts, _, _, _, _, _, hLogs := GetLayoutDimensions(width, height, statusView, showLogs)

	flex := flexbox.New(width, gridHeight)

	var (
		cellTranscript, cellAlts, cellLanes, cellMetrics, cellLogs *flexbox.Cell
	)

	if showLogs {
		row1 := flex.NewRow()
		cellTranscript = flexbox.NewCell(100, hTranscript).SetID("transcript")
		row1.AddCells(cellTranscript)

		rowLogs := flex.NewRow()
		cellLogs = flexbox.NewCell(100, hLogs).SetID("logs")
		rowLogs.AddCells(cellLogs)

		row2 := flex.NewRow()
		cellAlts = flexbox.NewCell(50, hAlts).SetID("alternatives")
		cellLanes = flexbox.NewCell(15, hAlts).SetID("lanes")
		cellMetrics = flexbox.NewCell(35, hAlts).SetID("metrics")
		row2.AddCells(cellAlts, cellLanes, cellMetrics)

		flex.AddRows([]*flexbox.Row{row1, rowLogs, row2})
	} else {
		row1 := flex.NewRow()
		cellTranscript = flexbox.NewCell(100, hTranscript).SetID("transcript")
		row1.AddCells(cellTranscript)

		row2 := flex.NewRow()
		cellAlts = flexbox.NewCell(50, hAlts).SetID("alternatives")
		cellLanes = flexbox.NewCell(15, hAlts).SetID("lanes")
		cellMetrics = flexbox.NewCell(35, hAlts).SetID("metrics")
		row2.AddCells(cellAlts, cellLanes, cellMetrics)

		flex.AddRows([]*flexbox.Row{row1, row2})
	}

	cellTranscript.SetContent("")
	cellAlts.SetContent("")
	cellLanes.SetContent("")
	cellMetrics.SetContent("")
	if cellLogs != nil {
		cellLogs.SetContent("")
	}

	_ = flex.Render()

	wTranscriptInner := cellTranscript.GetWidth() - 2
	if wTranscriptInner < 1 {
		wTranscriptInner = 1
	}
	hTranscriptInner := cellTranscript.GetHeight() - 2
	if hTranscriptInner < 1 {
		hTranscriptInner = 1
	}

	wAltsInner := cellAlts.GetWidth() - 2
	if wAltsInner < 1 {
		wAltsInner = 1
	}
	hAltsInner := cellAlts.GetHeight() - 2
	if hAltsInner < 1 {
		hAltsInner = 1
	}

	wLanesInner := cellLanes.GetWidth() - 2
	if wLanesInner < 1 {
		wLanesInner = 1
	}
	hLanesInner := cellLanes.GetHeight() - 2
	if hLanesInner < 1 {
		hLanesInner = 1
	}

	wMetricsInner := cellMetrics.GetWidth() - 2
	if wMetricsInner < 1 {
		wMetricsInner = 1
	}
	hMetricsInner := cellMetrics.GetHeight() - 2
	if hMetricsInner < 1 {
		hMetricsInner = 1
	}

	boxTranscript := styleTranscript.Width(wTranscriptInner).Height(hTranscriptInner).Render(transcriptView)
	boxAlts := styleAlts.Width(wAltsInner).Height(hAltsInner).Render(alternativesView)
	boxLanes := styleLanes.Width(wLanesInner).Height(hLanesInner).Render(lanesView)
	boxMetrics := styleMetrics.Width(wMetricsInner).Height(hMetricsInner).Render(metricsView)

	cellTranscript.SetContent(boxTranscript)
	cellAlts.SetContent(boxAlts)
	cellLanes.SetContent(boxLanes)
	cellMetrics.SetContent(boxMetrics)

	if cellLogs != nil {
		wLogsInner := cellLogs.GetWidth() - 2
		if wLogsInner < 1 {
			wLogsInner = 1
		}
		hLogsInner := cellLogs.GetHeight() - 2
		if hLogsInner < 1 {
			hLogsInner = 1
		}
		boxLogs := styleLogs.Width(wLogsInner).Height(hLogsInner).Render(logsView)
		cellLogs.SetContent(boxLogs)
	}

	mainGrid := flex.Render()

	headerStyle := lipgloss.NewStyle().
		Background(ColorDark).
		Foreground(ColorAccent).
		Bold(true).
		Padding(0, 1).
		Width(width)
	headerView := headerStyle.Render("🧬 DwarfStar4 Steering Inspector (ds4go-steer)")

	var b strings.Builder
	b.WriteString(headerView)
	b.WriteString("\n")
	b.WriteString(promptBox)
	b.WriteString("\n")
	b.WriteString(mainGrid)
	b.WriteString("\n")
	b.WriteString(statusView)

	return b.String()
}

// RenderPromptBox wraps the prompt text input with a border and splicing title.
func RenderPromptBox(width int, content string, title string, focused bool, innerHeight int) string {
	borderColor := ColorBorder
	if focused {
		borderColor = ColorActive
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(width - 2).
		Height(innerHeight)

	rendered := style.Render(content)
	lines := strings.Split(rendered, "\n")

	// Spliced top border with colors and title
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	var newTop string
	if title == "" {
		newTop = borderStyle.Render("╭" + strings.Repeat("─", width-2) + "╮")
	} else {
		titleText := " " + title + " "
		titleLen := len([]rune(titleText))
		leftDashCount := 1
		rightDashCount := (width - 2) - leftDashCount - titleLen
		if rightDashCount < 1 {
			newTop = borderStyle.Render("╭" + strings.Repeat("─", width-2) + "╮")
		} else {
			left := "╭" + strings.Repeat("─", leftDashCount)
			right := strings.Repeat("─", rightDashCount) + "╮"
			newTop = borderStyle.Render(left) + TitleStyle.Render(titleText) + borderStyle.Render(right)
		}
	}

	lines[0] = newTop
	return strings.Join(lines, "\n")
}
