package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/headerbar"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
	"github.com/charmbracelet/x/ansi"
)

// All dimensions include borders. The renderer and View share this geometry.
type workspaceLayout struct{ animationW, controlsW, mainH, activityH int }

func (m *model) layout() workspaceLayout {
	controls := min(32, max(20, m.width/3))
	activity := min(7, max(3, m.height/6))
	return workspaceLayout{m.width - controls, controls, max(3, m.height-7-activity), activity}
}

func (m *model) viewport() (int, int) {
	if m.fullscreen {
		return max(1, m.width), max(1, m.height)
	}
	l := m.layout()
	return max(1, l.animationW-2), max(1, l.mainH-2)
}

func (m *model) resizeViewport() tea.Cmd {
	m.input.SetWidth(max(1, m.width-4)) // border + "> "
	cols, rows := m.viewport()
	resize := m.pic.SetSize(cols, rows)
	m.dirty = true
	return tea.Batch(resize, m.renderCmd())
}

func (m *model) toggleFullscreen() tea.Cmd {
	m.fullscreen = !m.fullscreen
	return m.resizeViewport()
}

// Crop by terminal cells without wrapping Kitty's combining placeholder cells.
func fitCells(content string, width, height int) string {
	width, height = max(1, width), max(1, height)
	lines := strings.Split(content, "\n")
	out := make([]string, height)
	for i := range out {
		if i < len(lines) {
			out[i] = ansi.Truncate(lines[i], width, "")
		}
		out[i] += strings.Repeat(" ", max(0, width-lipgloss.Width(out[i])))
	}
	return strings.Join(out, "\n")
}

func pane(title, content string, width, height int, color string) string {
	style := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(color))
	box := style.Render(fitCells(content, width-2, height-2))
	lines := strings.Split(box, "\n")
	label := ansi.Truncate(" "+title+" ", width-2, "…")
	top := "┌" + label + strings.Repeat("─", max(0, width-2-lipgloss.Width(label))) + "┐"
	lines[0] = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(top)
	return strings.Join(lines, "\n")
}

func (m *model) parameterPanel(snap tools.Snapshot, width, height int) string {
	if len(snap.Source.Params) == 0 {
		return "No parameters."
	}
	selected := min(m.selected, len(snap.Source.Params)-1)
	count := max(1, height/2)
	start := max(0, selected-count+1)
	var lines []string
	for i := start; i < len(snap.Source.Params) && i < start+count; i++ {
		p, value := snap.Source.Params[i], snap.Values[i]
		mark := " "
		if i == selected {
			mark = "›"
		}
		number := fmt.Sprintf(" %.2f", value)
		name := ansi.Truncate(p.Name, max(1, width-2-lipgloss.Width(number)), "…")
		line := mark + " " + name + strings.Repeat(" ", max(0, width-2-lipgloss.Width(name+number))) + number
		barW := max(1, width-3)
		filled := min(barW, max(0, int((value-p.Min)/(p.Max-p.Min)*float32(barW))))
		bar := "  " + strings.Repeat("━", filled) + strings.Repeat("─", barW-filled)
		if i == selected && !m.input.Focused() {
			accent := lipgloss.NewStyle().Foreground(lipgloss.Color("99"))
			line, bar = accent.Render(line), accent.Render(bar)
		}
		lines = append(lines, line, bar)
	}
	return strings.Join(lines, "\n")
}

func tailText(text string, width, height int) string {
	text = cleanInspectText(text)
	// Keep per-frame wrapping bounded even after long model responses.
	limit := max(1024, width*height*4)
	if len(text) > limit {
		start := len(text) - limit
		for start < len(text) && !utf8.RuneStart(text[start]) {
			start++
		}
		text = text[start:]
	}
	lines := strings.Split(ansi.Wrap(text, max(1, width), ""), "\n")
	return strings.Join(lines[max(0, len(lines)-height):], "\n")
}

func (m *model) workspaceView() string {
	l := m.layout()
	snap := m.state.Snapshot()
	mode := "playing"
	if !m.playing {
		mode = "paused"
	}
	metrics := fmt.Sprintf("%.1f fps · %.1fs", m.fps, m.t)
	if m.rasterW > 0 {
		metrics = fmt.Sprintf("%.1f fps · %dx%d · %.1fs", m.fps, m.rasterW, m.rasterH, m.t)
	}
	header := headerbar.Layout(m.width, " trippad │ ", snap.Source.Name, metrics, " "+mode+" ")
	controlsColor, promptColor := "99", "240"
	if m.input.Focused() {
		controlsColor, promptColor = "240", "75"
	}
	animation := pane("ANIMATION", m.pic.View().Content, l.animationW, l.mainH, "240")
	parameters := pane("PARAMETERS", m.parameterPanel(snap, l.controlsW-2, l.mainH-2), l.controlsW, l.mainH, controlsColor)
	body := lipgloss.JoinHorizontal(lipgloss.Top, animation, parameters)

	output := "Replies and thinking appear here. Ctrl+T expands."
	if len(m.activity) > 0 {
		r := m.activity[len(m.activity)-1]
		output = strings.TrimSpace(r.thinking + "\n" + r.reply)
		if output == "" {
			output = "Waiting for model output…"
		}
	}

	outputW := m.width - l.controlsW
	activity := lipgloss.JoinHorizontal(lipgloss.Top,
		pane("THINKING / OUTPUT", tailText(output, outputW-2, l.activityH-2), outputW, l.activityH, "240"),
		pane(m.toolPaneTitle(l.controlsW), m.toolPaneBody(l.controlsW-2, l.activityH-2), l.controlsW, l.activityH, "240"))
	settings := "reasoning " + thinkLabel(m.thinkMode)
	if label := m.contextLabel(); label != "" {
		settings += " · " + label
	}
	footer := " Ctrl+F fullscreen · F3 gallery · Tab prompt · ? help · q quit"
	if m.input.Focused() {
		footer = " Enter send · Tab controls · Esc cancel · Ctrl+F fullscreen"
	}
	footer = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252")).Render(fitCells(footer, m.width, 1))
	return strings.Join([]string{header, fitCells(m.modelBar(), m.width, 1), body, activity,
		headerbar.Layout(m.width, "", m.status, "", " "+settings), pane("PROMPT", m.input.View(), m.width, 3, promptColor), footer}, "\n")
}

const workspaceHelp = `Animation
Ctrl+F / f        Toggle animation-only fullscreen (f when not typing)
Esc            Leave fullscreen; generation and draft are preserved
Space          Play / pause
[ / ]          Previous / next starter
R              Randomize controls
+ / -          Finer / coarser GPU raster (downscale 1..8; /downscale N)

Workspace
Tab            Switch between prompt and controls
↑ / ↓          Select a parameter
← / →          Adjust; Shift changes ten steps
F3             Shader gallery
Ctrl+O         Choose / change model
Ctrl+T         Thinking, output and tool calls
Ctrl+L         Current source
Ctrl+R         Reasoning level for the next request
Ctrl+N         Logs and diagnostics
Ctrl+Y         Copy the open inspector
F1 / ?         Help (Esc closes)
q / Ctrl+C     Quit (q only when not typing)

Open logs / inspectors
↑↓ / j k       Scroll a line
Space / b      Page down / up (also PgDn / PgUp)
Ctrl+D / U     Half page down / up
g / Home       Beginning
G / End        End; follow new output
Esc / q        Close pager, preserve prompt
Source: ←→ / Tab switches highlighted WGSL / JSON tabs

Prompt
Enter          Submit prompt or slash command
Esc            Cancel generation / leave prompt
/save FILE · /load FILE · /set NAME VALUE · /preset NAME
/memory · /compact · /gallery · /model · /fullscreen · /quit

Context: --ctx sets the session allocation (default 131072 tokens).
The prompt counter includes instructions, tools, memory and history.
/memory explains the measured usage; restart to change --ctx.

Fullscreen keeps Space, q and Ctrl+C active. Other editing keys are ignored.
Use Ctrl+F or Esc to return to the workspace.`
