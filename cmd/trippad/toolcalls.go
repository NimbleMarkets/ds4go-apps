package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/charmbracelet/x/ansi"
)

type toolStatus uint8

const (
	toolRunning toolStatus = iota // announced by the model, not yet answered
	toolOK
	toolFailed  // the tool answered "ERROR: …"
	toolSkipped // the run ended before the call executed
)

const (
	maxRoundCalls = 64
	maxArgsRunes  = 300
	maxResultRune = 600
)

// toolCall is one call the model made, from announcement to outcome. Text is
// sanitized and bounded when recorded, so rendering never has to be.
type toolCall struct {
	id, name, args, result string
	reason                 string // first line of a failure, without its "ERROR:" prefix
	status                 toolStatus
	started                time.Time
	took                   time.Duration
}

func (s toolStatus) mark() string {
	switch s {
	case toolOK:
		return "✓"
	case toolFailed:
		return "✗"
	case toolSkipped:
		return "–"
	}
	return "…"
}

// toolStats are session totals, kept apart from the 20 retained rounds so the
// counts do not shrink as old rounds are dropped.
type toolStats struct {
	calls, failed int
	byTool        map[string]int
	order         []string // tool names by first use
}

func (s *toolStats) add(name string) {
	if s.byTool == nil {
		s.byTool = map[string]int{}
	}
	if s.byTool[name] == 0 {
		s.order = append(s.order, name)
	}
	s.byTool[name]++
	s.calls++
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// summary is "3 calls · 1 failed · a×2, b×1".
func (s toolStats) summary() string {
	parts := []string{plural(s.calls, "call", "calls")}
	if s.failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.failed))
	}
	var per []string
	for _, name := range s.order {
		per = append(per, fmt.Sprintf("%s×%d", name, s.byTool[name]))
	}
	return strings.Join(append(parts, strings.Join(per, ", ")), " · ")
}

// oneLine sanitizes model- or tool-supplied text for a single display line.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(cleanInspectText(s)), " ")
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

// recordToolCalls notes the calls of a finished assistant message. They are
// known with their arguments only here: the stream shows just names.
func (m *model) recordToolCalls(calls []ds4.ToolCall) {
	if len(calls) == 0 {
		return
	}
	r := m.currentActivity()
	r.tools = nil // the stream's early names are replaced by the full records
	for _, call := range calls {
		m.toolStats.add(oneLine(call.Name, 80))
		if len(r.calls) < maxRoundCalls {
			r.calls = append(r.calls, toolCall{
				id: call.ID, name: oneLine(call.Name, 80), args: oneLine(call.Arguments, maxArgsRunes),
				status: toolRunning, started: time.Now(),
			})
		}
	}
}

// recordToolResults matches tool-role messages to announced calls by ID. A
// result with no announced call is ignored rather than guessed at.
func (m *model) recordToolResults(results []ds4.ChatMessage) {
	for _, res := range results {
		call := m.findToolCall(res.ToolCallID)
		if call == nil || call.status != toolRunning {
			continue
		}
		text := res.Content
		for _, part := range res.Parts {
			if part.Text != "" {
				text += part.Text
			}
			if part.Image != nil {
				text += " [image]"
			}
		}
		call.result = oneLine(text, maxResultRune)
		call.took = time.Since(call.started)
		call.status = toolOK
		if trimmed := strings.TrimSpace(text); strings.HasPrefix(trimmed, "ERROR:") {
			call.status = toolFailed
			m.toolStats.failed++
			first, _, _ := strings.Cut(strings.TrimPrefix(trimmed, "ERROR:"), "\n")
			call.reason = oneLine(first, maxResultRune)
		}
	}
}

func (m *model) findToolCall(id string) *toolCall {
	if id == "" {
		return nil
	}
	for i := len(m.activity) - 1; i >= 0; i-- {
		for j := range m.activity[i].calls {
			if m.activity[i].calls[j].id == id {
				return &m.activity[i].calls[j]
			}
		}
	}
	return nil
}

// abandonPendingTools marks calls the finished run never executed, such as
// those left over when the round limit stops it.
func (m *model) abandonPendingTools() {
	for i := range m.activity {
		for j := range m.activity[i].calls {
			if c := &m.activity[i].calls[j]; c.status == toolRunning {
				c.status = toolSkipped
			}
		}
	}
}

// detail is the inspector's multi-line view of one call.
func (c toolCall) detail() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s %s", c.status.mark(), c.name)
	switch {
	case c.status == toolRunning:
		b.WriteString(" · running")
	case c.status == toolSkipped:
		b.WriteString(" · not run")
	case c.took >= time.Millisecond:
		fmt.Fprintf(&b, " · %s", c.took.Round(time.Millisecond))
	}
	b.WriteString("\n")
	if c.args != "" {
		b.WriteString("    args: " + c.args + "\n")
	}
	if c.result != "" {
		b.WriteString("    → " + c.result + "\n")
	}
	return b.String()
}

// toolPaneTitle puts the totals in the pane title, which stays visible however
// short the pane is. It falls back to a compact form, then to the bare title.
func (m *model) toolPaneTitle(width int) string {
	const base = "TOOLS / LOG"
	if m.toolStats.calls == 0 {
		return base
	}
	f := m.toolStats.failed
	long, short := fmt.Sprintf("%s · %d", base, m.toolStats.calls), fmt.Sprintf("%s %d", base, m.toolStats.calls)
	if f > 0 {
		long += fmt.Sprintf(" · %d ✗", f)
		short += fmt.Sprintf("·%d✗", f)
	}
	for _, title := range []string{long, short} {
		if ansi.StringWidth(" "+title+" ") <= width-2 {
			return title
		}
	}
	return base
}

// Pane palette. Status carries the colour; names and the log stay quiet so a
// failure or a running call is the first thing the eye finds.
var (
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	failStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	runStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	skipStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	ruleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	reasonStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("167"))
)

func (s toolStatus) style() lipgloss.Style {
	switch s {
	case toolOK:
		return okStyle
	case toolFailed:
		return failStyle
	case toolSkipped:
		return skipStyle
	}
	return runStyle
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return ""
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.0fs", d.Seconds())
}

// callLine is "✓ name" with the duration right-aligned to the pane edge. A
// running call shows its elapsed time once it has taken a second.
func callLine(status toolStatus, name string, elapsed time.Duration, width int) string {
	if status == toolRunning && elapsed < time.Second {
		elapsed = 0
	}
	if status == toolSkipped {
		elapsed = 0
	}
	right := formatDuration(elapsed)
	nameW := width - 2
	if right != "" {
		nameW -= len(right) + 1
		if nameW < 6 { // too narrow for both: keep the name
			right, nameW = "", width-2
		}
	}
	name = ansi.Truncate(name, max(1, nameW), "…")
	shown := name
	if status != toolOK { // a finished call's name stays quiet; anything else is coloured
		shown = status.style().Render(name)
	}
	line := status.style().Render(status.mark()) + " " + shown
	if right != "" {
		pad := width - 2 - ansi.StringWidth(name) - len(right)
		line += strings.Repeat(" ", max(1, pad)) + dimStyle.Render(right)
	}
	return line
}

// block is a call's lines for the pane: the call, then a failure's reason.
// Without room for the reason only the call line is returned.
func (c toolCall) block(width int, withReason bool) []string {
	elapsed := c.took
	if c.status == toolRunning {
		elapsed = time.Since(c.started)
	}
	lines := []string{callLine(c.status, c.name, elapsed, width)}
	if withReason && c.status == toolFailed && c.reason != "" {
		lines = append(lines, "  "+reasonStyle.Render(ansi.Truncate(c.reason, max(1, width-2), "…")))
	}
	return lines
}

// toolPaneLines is the newest calls, oldest first, in at most height lines.
// A call is never split across the crop; older calls fall off the top, and a
// reason is dropped before its call is.
func (m *model) toolPaneLines(width, height int) []string {
	var out []string
	add := func(block []string) bool {
		if len(out)+len(block) > height {
			if len(out)+1 > height {
				return false
			}
			block = block[:1]
		}
		out = append(append([]string(nil), block...), out...)
		return true
	}
	for i := len(m.activity) - 1; i >= 0; i-- {
		r := m.activity[i]
		if len(r.calls) == 0 {
			// Names seen in the stream before the message supplies the calls.
			for j := len(r.tools) - 1; j >= 0; j-- {
				if !add([]string{callLine(toolRunning, oneLine(r.tools[j], 80), 0, width)}) {
					return out
				}
			}
			continue
		}
		for j := len(r.calls) - 1; j >= 0; j-- {
			if !add(r.calls[j].block(width, true)) {
				return out
			}
		}
	}
	return out
}

// toolPaneBody lays out the TOOLS / LOG pane: the dimmed log on top, a labelled
// divider, then the calls. A pane too short for the log and divider shows
// calls alone; with no calls it is the plain log or a hint.
func (m *model) toolPaneBody(width, height int) string {
	width, height = max(1, width), max(1, height)
	var logText string
	if len(m.log) > 0 {
		logText = strings.Join(m.log[max(0, len(m.log)-8):], "\n")
	}
	calls := m.toolPaneLines(width, height)
	if len(calls) == 0 {
		if logText == "" {
			logText = "No tools yet. Ctrl+N logs."
		}
		return dimStyle.Render(tailText(logText, width, height))
	}
	var out []string
	if room := height - len(calls); room >= 2 && logText != "" {
		for _, l := range strings.Split(tailText(logText, width, room-1), "\n") {
			out = append(out, dimStyle.Render(l))
		}
		label := "─ calls "
		out = append(out, ruleStyle.Render(label+strings.Repeat("─", max(0, width-ansi.StringWidth(label)))))
	}
	return strings.Join(append(out, calls...), "\n")
}
