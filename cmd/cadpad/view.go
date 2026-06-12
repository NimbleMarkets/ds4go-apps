package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/headerbar"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
	"github.com/charmbracelet/x/ansi"
)

const (
	headerH   = 1
	helpH     = 1
	footerH   = 3
	minListW  = 18
	minPropsW = 22
	minViewW  = 30
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	objStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	currStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func (m model) View() tea.View {
	if m.width == 0 {
		return tea.NewView("initializing...")
	}
	if m.showLog {
		return tea.NewView(m.logOverlay())
	}
	if m.showHelp {
		v := tea.NewView(m.helpView())
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	if m.cachedView != nil && *m.cachedView != "" {
		v := tea.NewView(*m.cachedView)
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	hdr := m.header()
	body := m.bodyView()
	boxes := m.bottomBoxesView()
	foot := m.footerView()
	help := m.helpLine()

	res := lipgloss.JoinVertical(lipgloss.Left, hdr, body, boxes, foot, help)
	if m.cachedView != nil {
		*m.cachedView = res
	}

	v := tea.NewView(res)
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m model) header() string {
	brand := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Render("nm cadpad")

	var info []string
	if cur := m.w.Current(); cur != "" {
		info = append(info, cur)
	}
	info = append(info, fmt.Sprintf("%d objs", len(m.w.Names())))
	if m.thinkMode != ds4.ThinkNone {
		info = append(info, "think:"+thinkModeLabel(m.thinkMode))
	}

	kitty := m.pic.KittySupported()
	var kittyBadge string
	switch kitty {
	case picture.KittyCapabilitySupported:
		kittyBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("Kitty ✓")
	case picture.KittyCapabilityUnsupported:
		kittyBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("glyph")
	default:
		kittyBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("Kitty ?")
	}

	gpuBadge := ""
	if m.engine != nil {
		gpuBadge = engineinit.Badge(engineinit.StatusReady)
	} else if m.lifecycle.status == engineinit.StatusError {
		gpuBadge = engineinit.Badge(engineinit.StatusError)
	} else if m.inferencing {
		gpuBadge = engineinit.Badge(engineinit.StatusOpening)
	} else {
		gpuBadge = engineinit.Badge(engineinit.StatusDormant)
	}

	left := brand + "  " + strings.Join(info, "  ")

	status := ""
	if m.inferencing {
		status = m.robotSpinner()
	}

	raw := headerbar.Layout(m.width, left, status, gpuBadge, kittyBadge)

	return lipgloss.NewStyle().
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("252")).
		Width(m.width).
		Render(raw)
}

func (m model) bodyView() string {
	listW := max(minListW, m.width/5)
	propsW := max(minPropsW, m.width/5)
	viewW := max(minViewW, m.width-listW-propsW-4)

	list := m.objectsList(listW)
	view := m.viewportView(viewW)
	props := m.propsView(propsW)

	return lipgloss.JoinHorizontal(lipgloss.Top, list, view, props)
}

// viewportInnerSize returns the column and row count available for the
// picture widget inside the bordered viewport panel.
func (m model) viewportInnerSize() (cols, rows int) {
	listW := max(minListW, m.width/5)
	propsW := max(minPropsW, m.width/5)
	viewW := max(minViewW, m.width-listW-propsW-4)
	cols = max(8, viewW-2)     // subtract border
	rows = max(6, m.bodyH()-3) // subtract border + header line
	return cols, rows
}

func (m model) objectsList(w int) string {
	names := m.w.Names()
	if len(names) == 0 {
		return dimStyle.Render("  (no objects)\n  pgup/pgdown: browse previous .lua\n  describe what to build...")
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(" Objects\n"))
	for i, n := range names {
		style := objStyle
		if i == m.selected || n == m.w.Current() {
			style = currStyle
		}
		pfx := "  "
		if n == m.w.Current() {
			pfx = "▶ "
		}
		line := pfx + n
		b.WriteString(style.Width(w-2).Render(line) + "\n")
	}
	return lipgloss.NewStyle().Width(w).MaxHeight(m.bodyH()).Render(b.String())
}

func (m model) viewportView(w int) string {
	projLabel := strings.ToUpper(string(m.proj))
	if m.proj == render.ProjAngle {
		projLabel = "3D"
	}
	if m.resIndex > 0 {
		projLabel += fmt.Sprintf("@%d", resPresets[m.resIndex])
	}
	cur := m.w.Current()
	if cur == "" {
		cur = "(none)"
	}

	script := "(no script)"
	if m.luaEntryIndex >= 0 && m.luaEntryIndex < len(m.luaEntries) {
		script = m.luaEntries[m.luaEntryIndex].filename
	}

	hint := "(1/2/3/4 proj  p r  pgup/pgdown)"
	if m.proj == render.ProjAngle {
		hint = "(←→ orbit  ↑↓ tilt  shift+arrows pan  +/− zoom  0 reset)"
	}
	header := fmt.Sprintf("%s view · %s  · %s  %s", projLabel, cur, script, hint)
	v := m.pic.View()
	content := ""
	if v.Content != "" {
		content = v.Content
	} else if len(m.w.Names()) > 0 {
		content = dimStyle.Render("(preview loading… press p to refresh)")
	}
	borderColor := "8"
	if m.focus == focusViewport {
		borderColor = "12"
	}
	return lipgloss.NewStyle().
		Width(w).
		Height(m.bodyH()).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Render(lipgloss.JoinVertical(lipgloss.Left, dimStyle.Render(header), content))
}

func (m model) propsView(w int) string {
	cur := m.w.Current()
	if cur == "" {
		return dimStyle.Width(w).Render(" no selection\n\nj/k: nav objects\nenter: set current\ntab: cycle focus\nT/L: toggle boxes\n\nViewport (focused):\n1/2/3/4: projection\np: preview  r: refresh\n←→↑↓: orbit (3D)\nshift+arrows: pan (3D)\n+/−: zoom  0: reset\n[ ]: resolution\npgup/pgdown: lua history\n\nBox (focused):\n↑↓: scroll\npgup/pgdown: page scroll\n\ns: save\nt: think mode\nctrl+n: full logs\n?: help")
	}
	s, meta, ok := m.w.Get(cur)
	if !ok {
		return errStyle.Render("missing?")
	}
	bb, _ := m.w.Bounds(cur)
	vol := (bb.Max.X - bb.Min.X) * (bb.Max.Y - bb.Min.Y) * (bb.Max.Z - bb.Min.Z)
	txt := fmt.Sprintf("%s\n\nbbox:\n  x %.2f..%.2f\n  y %.2f..%.2f\n  z %.2f..%.2f\nvol≈%.1f\n\nupdated: %s",
		cur,
		bb.Min.X, bb.Max.X,
		bb.Min.Y, bb.Max.Y,
		bb.Min.Z, bb.Max.Z,
		vol,
		meta.Updated.Format("15:04:05"),
	)
	_ = s

	return lipgloss.NewStyle().Width(w).MaxHeight(m.bodyH()).Render(txt)
}

func (m model) bottomBoxesView() string {
	if !m.showThinking && !m.showLuaOutput && !m.showSource {
		return ""
	}

	w := m.width
	var parts []string

	// LLM Output and Lua Source share the top row side by side; either one
	// alone takes the full width.
	if m.showThinking && m.showSource {
		w = m.width / 2
	}

	if m.showThinking {
		var content string
		if len(m.reasoningLog) > 0 {
			visibleCount := 8
			total := len(m.reasoningLog)
			start := 0
			if total > visibleCount {
				start = total - visibleCount - m.thinkScroll
				if start < 0 {
					start = 0
				}
			}
			end := start + visibleCount
			if end > total {
				end = total
			}
			lines := m.reasoningLog[start:end]
			content = sanitizeForDisplay(strings.Join(lines, "\n"))
		} else if m.lastThinking != "" {
			content = sanitizeForDisplay(m.lastThinking)
		} else if m.lastLuaOutput != "" {
			content = sanitizeForDisplay(m.lastLuaOutput)
		} else {
			content = dimStyle.Render("(no LLM output yet)")
		}
		borderColor := "13"
		if m.focus == focusThinking {
			borderColor = "5"
		}
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(borderColor)).
			Width(w).
			MaxHeight(12).
			Padding(0, 1).
			Render(lipgloss.JoinVertical(lipgloss.Left,
				"LLM Output (toggle with T)",
				content,
			))
		parts = append(parts, box)
	}

	if m.showSource {
		srcW := m.width - w
		if !m.showThinking {
			srcW = m.width
		}
		src := m.sourcePanel(srcW)
		if m.showThinking && len(parts) == 1 {
			parts[0] = lipgloss.JoinHorizontal(lipgloss.Top, parts[0], src)
		} else {
			parts = append(parts, src)
		}
	}

	if m.showLuaOutput {
		w := m.width // the lua_run output box always spans the full width
		content := sanitizeForDisplay(m.lastLuaOutput)
		if content != "" {
			lines := strings.Split(content, "\n")
			visibleCount := 4
			total := len(lines)
			start := 0
			if total > visibleCount {
				start = total - visibleCount - m.luaScroll
				if start < 0 {
					start = 0
				}
			}
			end := start + visibleCount
			if end > total {
				end = total
			}
			content = strings.Join(lines[start:end], "\n")
		}
		if content == "" {
			content = dimStyle.Render("(no lua output yet — use lua_run)")
		}
		borderColor := "10"
		if m.focus == focusLuaOutput {
			borderColor = "2"
		}
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(borderColor)).
			Width(w).
			MaxHeight(6).
			Padding(0, 1).
			Render(lipgloss.JoinVertical(lipgloss.Left,
				"Lua Output (toggle with L)",
				content,
			))
		parts = append(parts, box)
	}

	return strings.Join(parts, "\n")
}

func (m model) robotSpinner() string {
	robot := "🤖"
	track := 12
	cycle := track * 2
	p := m.spinnerFrame % cycle
	if p > track {
		p = cycle - p
	}
	left := strings.Repeat(" ", p)
	right := strings.Repeat(" ", track-p)
	spinner := "[" + left + robot + right + "]"

	return lipgloss.NewStyle().
		Background(lipgloss.Color("63")).
		Foreground(lipgloss.Color("15")).
		Bold(true).
		Render(spinner)
}

func (m model) helpLine() string {
	return dimStyle.Render(" " + m.keymap().FooterText(m.input.Focused()))
}

// flattenLine collapses all whitespace runs (including newlines) to single
// spaces so a string can safely occupy one terminal row.
func flattenLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// footerStatusLine renders the one-row status + recent-tool-history bar.
// History entries can carry full multi-line model output (toolDoneMsg
// appends the assistant's final text), so every part is flattened and the
// line truncated — one stray newline here multiplies the footer's height
// and shears the whole layout.
func (m model) footerStatusLine() string {
	hist := ""
	if len(m.toolHistory) > 0 {
		last := m.toolHistory[max(0, len(m.toolHistory)-2):]
		flat := make([]string, 0, len(last))
		for _, h := range last {
			flat = append(flat, flattenLine(h))
		}
		hist = dimStyle.Render(" " + strings.Join(flat, " | "))
	}
	status := okStyle.Render(flattenLine(m.status))
	if m.lifecycle.status == engineinit.StatusError {
		status = errStyle.Render("engine: " + flattenLine(m.lifecycle.err.Error()))
	}
	line := status + hist
	if m.width > 0 {
		line = ansi.Truncate(line, m.width-1, "…")
	}
	return line
}

func (m model) footerView() string {
	inputView := m.input.View()
	errLine := ""
	if m.lastErr != "" {
		errLine = errStyle.Render(" " + ansi.Truncate(flattenLine(m.lastErr), m.width-2, "…"))
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		inputView,
		lipgloss.NewStyle().Width(m.width).Render(m.footerStatusLine()),
		errLine,
	)
}

func (m model) helpView() string {
	help := `cadpad — LLM CAD scratchpad

Keys
  e             edit / target the prompt window (command → edit mode)
  esc           blur prompt window (edit → command mode)
  enter         submit LLM command (in edit mode) or set current (command mode)
  j/k           navigate object list (always)
  tab           cycle focus: viewport → LLM box → lua box → viewport

Viewport focus
  1/2/3/4       XY / XZ / YZ / 3D projection
  [ / ]         decrease / increase preview resolution
  ←→ ↑↓         orbit camera (3D angle mode only)
  shift+arrows  pan camera (3D angle mode only)
  + / −         zoom in / out (3D angle mode only)
  0             reset camera (3D angle mode only)
  p             refresh preview of current
  r             clear render cache + refresh
  pgup/pgdown   browse previous .lua files
  v             view active .lua source (syntax highlighted)

Box focus (LLM output / lua source / lua output)
  ↑ / ↓         scroll content
  pgup/pgdown   page scroll

Global
  s             quick-save history to cadpad-session.cad.json
  t             cycle reasoning effort (OFF / HIGH / MAX)
  T             toggle LLM output box
  L             toggle lua output box
  ctrl+n        show/hide full log overlay (engine + thinking history)
  q / ctrl-c    quit
  ? / h         toggle this help

The bottom prompt box follows the same Command/Edit mode model as glyphpad and svgpad.

Commands (bottom bar)
  Plain text is sent to the LLM with full cad_* tool suite.
  Slash commands work without LLM:
    /create sphere ball r=2.5
    /boolean diff base cutter blend=0.1
    /transform post translate 0 0 3
    /group assembly base post
    /export name.stl
    /save mysession.cad.json
    /load mysession.cad.json
    /clear

LLM tips
  "create a lua script that builds a plate with an array of holes using a loop"
  "write a reusable function for rounded posts and use it"
  "use lua_run to execute your script and see the result in the viewport"

The world is a live replayable history. Save often.`
	return lipgloss.NewStyle().Padding(1, 2).Render(help)
}

func (m model) resize() model {
	m.input.SetWidth(max(20, m.width-4))
	return m
}

func (m model) bodyH() int {
	extra := m.bottomBoxesHeight()
	return max(8, m.height-headerH-helpH-footerH-2-extra)
}

func (m model) bottomBoxesHeight() int {
	if !m.showThinking && !m.showLuaOutput && !m.showSource {
		return 0
	}
	h := 0
	if m.showThinking || m.showSource {
		h += 13 // title + border + up to 12 lines; LLM + source share this row
	}
	if m.showLuaOutput {
		h += 7
	}
	if (m.showThinking || m.showSource) && m.showLuaOutput {
		h += 1
	}
	return h
}

func (m model) logPageSize() int {
	h := m.height - 6
	if h < 1 {
		return 1
	}
	return h
}

func (m model) logScrollBy(delta int) int {
	total, innerH := m.logVisibleMetrics()
	maxTop := total - innerH
	if maxTop < 0 {
		maxTop = 0
	}
	top := m.logTop
	if top < 0 {
		top = maxTop
	}
	top += delta
	if top < 0 {
		top = 0
	}
	if top >= maxTop {
		return -1
	}
	return top
}

func (m model) logVisibleMetrics() (total, innerH int) {
	innerW := m.width - 6
	if innerW < 10 {
		innerW = 10
	}
	innerH = m.height - 6
	if innerH < 1 {
		innerH = 1
	}
	lines := m.logBuf.Lines()
	if len(lines) == 0 {
		return 0, innerH
	}
	wrapped := lipgloss.NewStyle().Width(innerW).Render(strings.Join(lines, "\n"))
	return strings.Count(wrapped, "\n") + 1, innerH
}

// sanitizeForDisplay strips escape sequences and control characters from
// model- or tool-produced text so it cannot move the cursor, erase lines, or
// reset to column 0 when rendered inside a bordered box. lipgloss clips the
// printable width/height of content but passes embedded control codes straight
// through to the terminal, so untrusted multi-line output (model reasoning,
// tool results) must be cleaned first or it tears the composed frame.
func sanitizeForDisplay(s string) string {
	s = ansi.Strip(s) // remove CSI/OSC/SGR escape sequences
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")   // lone CR would reset to column 0
	s = strings.ReplaceAll(s, "\t", "    ") // tabs render terminal-width; normalize
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r >= 0x20 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizeLines applies sanitizeForDisplay to each line of model/tool output.
func sanitizeLines(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = sanitizeForDisplay(ln)
	}
	return out
}

func titledBox(rendered, title string) string {
	if title != "" {
		title = " " + title + " "
	}
	lines := strings.SplitN(rendered, "\n", 2)
	if len(lines) == 0 {
		return rendered
	}
	top := []rune(lines[0])
	label := []rune(title)
	if len(top) < len(label)+4 {
		return rendered
	}
	newTop := string(top[:2]) + titleStyle.Render(string(label)) + string(top[2+len(label):])
	if len(lines) == 2 {
		return newTop + "\n" + lines[1]
	}
	return newTop
}

func (m model) logOverlay() string {
	engineLines := sanitizeLines(m.logBuf.Lines())
	if len(engineLines) == 0 {
		engineLines = []string{dimStyle.Render("(no libds4 diagnostics yet)")}
	}

	var genSection []string
	if len(m.generationLog) > 0 {
		genSection = append(genSection, "", dimStyle.Render("── Generation Log ──"))
		genSection = append(genSection, sanitizeLines(m.generationLog)...)
	}

	var reasoningSection []string
	if len(m.reasoningLog) > 0 {
		reasoningSection = append(reasoningSection, "", dimStyle.Render("── Model Reasoning ──"))
		reasoningSection = append(reasoningSection, sanitizeLines(m.reasoningLog)...)
	}

	allContent := append([]string(nil), engineLines...)
	allContent = append(allContent, genSection...)
	allContent = append(allContent, reasoningSection...)

	if len(allContent) == 0 {
		allContent = []string{dimStyle.Render("(no logs yet)")}
	}

	innerW := m.width - 6
	if innerW < 10 {
		innerW = 10
	}
	innerH := m.height - 6
	if innerH < 1 {
		innerH = 1
	}

	wrapped := lipgloss.NewStyle().Width(innerW).Render(strings.Join(allContent, "\n"))
	allLines := strings.Split(wrapped, "\n")

	total := len(allLines)
	maxTop := total - innerH
	if maxTop < 0 {
		maxTop = 0
	}

	var start int
	following := m.logTop < 0
	if following {
		start = maxTop
	} else {
		start = m.logTop
		if start > maxTop {
			start = maxTop
		}
	}
	end := start + innerH
	if end > total {
		end = total
	}

	body := strings.Join(allLines[start:end], "\n")

	mode := "TAIL"
	if !following {
		mode = "FROZEN"
	}
	hint := fmt.Sprintf("  ↑/↓/j/k · pgup/pgdn  ·  esc close   [%s %d/%d]   (engine + thinking)", mode, start, maxTop)
	body += "\n\n" + dimStyle.Render(hint)

	title := "cadpad · logs (engine + thinking)"
	if m.thinkMode != ds4.ThinkNone {
		title += " [reason:" + thinkModeLabel(m.thinkMode) + "]"
	}

	box := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Render(body), title)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
