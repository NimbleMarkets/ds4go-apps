package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go/dsml"
	"github.com/charmbracelet/x/ansi"
)

type inspector struct {
	kind                     string
	top                      int // -1 follows streaming output
	tab                      int
	notice                   string
	cacheText, cacheLanguage string
	cacheWidth               int
	cacheLines               []string
}

type activityRound struct {
	number          int
	thinking, reply string
	tools           []string
}

func thinkLabel(mode ds4.ThinkMode) string {
	switch mode {
	case ds4.ThinkHigh:
		return "high"
	case ds4.ThinkMax:
		return "max"
	default:
		return "off"
	}
}

func boundedActivity(s string) string {
	const limit = 32000
	r := []rune(s)
	if len(r) > limit {
		return "… earlier text omitted …\n" + string(r[len(r)-limit:])
	}
	return s
}

func (m *model) startActivityRound(round int) {
	m.activity = append(m.activity, activityRound{number: round + 1})
	if len(m.activity) > 20 {
		m.activity = append([]activityRound(nil), m.activity[len(m.activity)-20:]...)
	}
}

func (m *model) currentActivity() *activityRound {
	if len(m.activity) == 0 {
		m.startActivityRound(0)
	}
	return &m.activity[len(m.activity)-1]
}

func (m *model) applyStream(ev dsml.StreamEvent) {
	r := m.currentActivity()
	switch ev.Type {
	case dsml.EventReasoningDelta:
		r.thinking = boundedActivity(r.thinking + ev.Delta)
	case dsml.EventContentDelta:
		r.reply = boundedActivity(r.reply + ev.Delta)
	case dsml.EventToolCallStart:
		if len(r.tools) < 64 {
			r.tools = append(r.tools, boundedActivity(ev.Name))
		}
	}
}

func (m *model) finishActivity(msg ds4.ChatMessage) {
	r := m.currentActivity()
	// Parsed content is authoritative; don't append a second copy of deltas.
	if msg.ReasoningContent != "" {
		r.thinking = boundedActivity(msg.ReasoningContent)
	}
	if msg.Content != "" {
		r.reply = boundedActivity(msg.Content)
	}
}

func (m *model) activityText() string {
	mode := m.activeThinkMode
	if len(m.activity) == 0 && m.gen == nil {
		mode = m.thinkMode
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Reasoning: %s · Ctrl+R changes the next request\n", thinkLabel(mode))
	if len(m.activity) == 0 {
		b.WriteString("\nNo model output yet. Replies and tool activity appear here live.\n")
	}
	if mode == ds4.ThinkNone {
		b.WriteString("Reasoning is off; Ctrl+R enables it for your next request.\n")
	}
	for _, r := range m.activity {
		fmt.Fprintf(&b, "\n── Round %d ──\n", r.number)
		if r.thinking != "" {
			b.WriteString("Thinking\n" + r.thinking + "\n")
		}
		if r.reply != "" {
			b.WriteString("Reply\n" + r.reply + "\n")
		}
		if len(r.tools) > 0 {
			b.WriteString("Tools: " + strings.Join(r.tools, ", ") + "\n")
		}
	}
	return b.String()
}

func (m *model) sourceText() (title, text string) {
	snap := m.state.Snapshot()
	names := []string{"Shade body", "Full WGSL", "Preset JSON"}
	title = fmt.Sprintf("Source · %s · %s (%d/3)", snap.Source.Name, names[m.inspect.tab], m.inspect.tab+1)
	switch m.inspect.tab {
	case 0:
		text = snap.Source.ShadeBody
	case 1:
		text, _ = shader.BuildWGSL(snap.Source)
	case 2:
		data, _ := json.MarshalIndent(params.Preset{Name: snap.Source.Name, Shader: snap.Source.ShadeBody, Params: snap.Source.Params, Values: snap.Named}, "", "  ")
		text = string(data)
	}
	return title, text
}

func (m *model) inspectorContent() (string, string) {
	if m.inspect.kind == "help" {
		return "Trippad · keys & commands", workspaceHelp
	}
	if m.inspect.kind == "memory" {
		return "Memory & context", m.memoryText
	}
	if m.inspect.kind == "source" {
		return m.sourceText()
	}
	return "Thinking & output · " + m.modelDisplayName(), m.activityText()
}

func cleanInspectText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

func (m *model) inspectorLines() []string {
	_, text := m.inspectorContent()
	w, _ := padui.PagerSize(m.width, m.height)
	language := ""
	if m.inspect.kind == "source" {
		language = "wgsl"
		if m.inspect.tab == 2 {
			language = "json"
		}
	}
	if m.inspect.cacheLines != nil && m.inspect.cacheText == text && m.inspect.cacheLanguage == language && m.inspect.cacheWidth == w {
		return m.inspect.cacheLines
	}
	raw := text
	text = strings.ReplaceAll(cleanInspectText(text), "\t", "    ")
	if language != "" {
		text = padui.Highlight(text, language)
	}
	m.inspect.cacheText, m.inspect.cacheLanguage, m.inspect.cacheWidth = raw, language, w
	if language == "" {
		m.inspect.cacheLines = strings.Split(ansi.Wrap(text, w, ""), "\n")
	} else {
		m.inspect.cacheLines = nil
		for _, line := range strings.Split(text, "\n") {
			// Cut carries ANSI state into each visible segment, including when
			// scrolling into the middle of a long comment or string.
			for start := 0; start < max(1, ansi.StringWidth(line)); start += w {
				m.inspect.cacheLines = append(m.inspect.cacheLines, ansi.Cut(line, start, start+w))
			}
		}
	}
	return m.inspect.cacheLines
}

func (m *model) inspectorView() string {
	title, _ := m.inspectorContent()
	lines := m.inspectorLines()
	hint := "↑↓/jk · Space/b page · g/G start/follow · Ctrl+Y copy · Esc/q close"
	if m.inspect.kind == "source" {
		hint = "←→/Tab source · Space/b page · g/G start/end · Ctrl+Y copy · Esc/q close"
	}
	if m.inspect.notice != "" {
		hint = m.inspect.notice
	}
	return padui.Pager(m.width, m.height, cleanInspectText(title), lines, m.inspect.top, hint)
}

func (m *model) inspectorKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	k := msg.String()
	if k == "f1" || (m.inspect.kind == "help" && k == "?") {
		if m.inspect.kind == "help" {
			m.inspect.kind = ""
		} else {
			m.inspect = inspector{kind: "help"}
		}
		return true, nil
	}
	if k == "ctrl+r" {
		switch m.thinkMode {
		case ds4.ThinkNone:
			m.thinkMode = ds4.ThinkHigh
		case ds4.ThinkHigh:
			m.thinkMode = ds4.ThinkMax
		default:
			m.thinkMode = ds4.ThinkNone
		}
		m.status = "Next request reasoning: " + thinkLabel(m.thinkMode)
		m.inspect.notice = m.status
		return true, nil
	}
	kind := ""
	if k == "ctrl+t" || (!m.input.Focused() && (k == "T" || k == "shift+t")) {
		kind = "thinking"
	}
	if k == "ctrl+l" || (!m.input.Focused() && (k == "L" || k == "shift+l")) {
		kind = "source"
	}
	if kind != "" {
		if m.inspect.kind == kind {
			m.inspect.kind = ""
		} else {
			m.inspect = inspector{kind: kind}
			if kind == "thinking" {
				m.inspect.top = -1
			}
		}
		m.showLog = false
		return true, nil
	}
	if k == "ctrl+n" {
		m.inspect.kind = ""
		return false, nil
	}
	if m.inspect.kind == "" {
		return false, nil
	}
	if k == "ctrl+y" {
		title, text := m.inspectorContent()
		var cmd tea.Cmd
		m.inspect.notice, cmd = padui.Copy(title, cleanInspectText(text))
		return true, cmd
	}
	m.inspect.notice = ""
	_, page := padui.PagerSize(m.width, m.height)
	m.inspect.top = padui.PagerKey(k, m.inspect.top, len(m.inspectorLines()), page)
	switch k {
	case "esc", "q":
		m.inspect.kind = ""
	case "left", "right", "tab", "shift+tab":
		if m.inspect.kind == "source" {
			delta := 1
			if k == "left" || k == "shift+tab" {
				delta = -1
			}
			m.inspect.tab = (m.inspect.tab + delta + 3) % 3
			m.inspect.top = 0
		}
	}
	return true, nil
}
