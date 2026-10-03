package main

import (
	"fmt"
	"strings"
	"time"

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
		if strings.HasPrefix(strings.TrimSpace(text), "ERROR:") {
			call.status = toolFailed
			m.toolStats.failed++
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

// recentToolLines lists the newest calls oldest-first for the small pane, so
// the latest one is the last line and survives the pane's tail cropping.
func (m *model) recentToolLines(n int) []string {
	var lines []string
	for i := len(m.activity) - 1; i >= 0 && len(lines) < n; i-- {
		for j := len(m.activity[i].calls) - 1; j >= 0 && len(lines) < n; j-- {
			c := m.activity[i].calls[j]
			lines = append([]string{c.status.mark() + " " + c.name}, lines...)
		}
	}
	return lines
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
