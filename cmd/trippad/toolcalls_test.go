package main

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/charmbracelet/x/ansi"
)

func announceCalls(m *model, round int, calls ...ds4.ToolCall) {
	m.Update(driverMsg{event: bubble.RoundStartedEvent{Round: round}})
	m.Update(driverMsg{event: bubble.AssistantMessageEvent{Message: ds4.ChatMessage{Role: "assistant", ToolCalls: calls}}})
}

func returnResults(m *model, results ...ds4.ChatMessage) {
	m.Update(driverMsg{event: bubble.ToolResultsEvent{Results: results}})
}

func TestToolCallsAreTrackedWithArgumentsAndOutcomes(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 30
	announceCalls(m, 0,
		ds4.ToolCall{ID: "c1", Name: "trip_set_param", Arguments: `{"name":"speed","value":2}`},
		ds4.ToolCall{ID: "c2", Name: "trip_set_shader", Arguments: `{"mode":"edit"}`},
	)
	// Announced but not yet executed.
	text := m.activityText()
	for _, want := range []string{"trip_set_param", `"speed"`, "trip_set_shader", "running"} {
		if !strings.Contains(text, want) {
			t.Fatalf("pending calls missing %q:\n%s", want, text)
		}
	}
	returnResults(m,
		ds4.ChatMessage{Role: "tool", ToolCallID: "c1", Content: `{"ok":true}`},
		ds4.ChatMessage{Role: "tool", ToolCallID: "c2", Content: "ERROR: 3:4: unknown identifier foo"},
	)
	text = m.activityText()
	for _, want := range []string{"✓ trip_set_param", "✗ trip_set_shader", `{"ok":true}`, "unknown identifier foo", "2 calls", "1 failed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("finished calls missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "running") {
		t.Fatalf("finished calls still marked running:\n%s", text)
	}
}

func TestToolTotalsSpanRoundsAndCountPerTool(t *testing.T) {
	m := testModel(t)
	announceCalls(m, 0, ds4.ToolCall{ID: "a", Name: "trip_set_param", Arguments: `{}`})
	returnResults(m, ds4.ChatMessage{Role: "tool", ToolCallID: "a", Content: "ok"})
	announceCalls(m, 1,
		ds4.ToolCall{ID: "b", Name: "trip_set_param", Arguments: `{}`},
		ds4.ToolCall{ID: "c", Name: "trip_describe", Arguments: `{}`},
	)
	returnResults(m,
		ds4.ChatMessage{Role: "tool", ToolCallID: "b", Content: "ok"},
		ds4.ChatMessage{Role: "tool", ToolCallID: "c", Content: "ok"},
	)
	text := m.activityText()
	for _, want := range []string{"3 calls", "trip_set_param×2", "trip_describe×1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("totals missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "failed") {
		t.Fatalf("reported failures when every call succeeded:\n%s", text)
	}
}

func TestToolPaneShowsCallsAndCountsInTitle(t *testing.T) {
	for _, size := range [][2]int{{48, 16}, {80, 24}, {100, 32}} {
		m := testModel(t)
		m.width, m.height = size[0], size[1]
		announceCalls(m, 0,
			ds4.ToolCall{ID: "c1", Name: "trip_randomize", Arguments: `{}`},
			ds4.ToolCall{ID: "c2", Name: "trip_set_shader", Arguments: `{}`},
		)
		returnResults(m,
			ds4.ChatMessage{Role: "tool", ToolCallID: "c1", Content: "ok"},
			ds4.ChatMessage{Role: "tool", ToolCallID: "c2", Content: "ERROR: bad"},
		)
		view := ansi.Strip(m.View().Content)
		// Long title where the pane is wide enough, compact where it is not.
		title := "TOOLS / LOG 2·1✗"
		if m.layout().controlsW >= 25 {
			title = "TOOLS / LOG · 2 · 1 ✗"
		}
		if !strings.Contains(view, title) {
			t.Fatalf("%v: title lacks counts %q:\n%s", size, title, view)
		}
		// The newest calls are the tail of the pane, so they survive its
		// smallest size.
		if !strings.Contains(view, "✗ trip_set_shader") {
			t.Fatalf("%v: pane lacks the latest call:\n%s", size, view)
		}
	}
}

func TestToolPaneTitleHasNoCountsBeforeAnyCall(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 30
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "TOOLS / LOG ·") {
		t.Fatalf("title shows counts with no calls:\n%s", view)
	}
}

func TestUnexecutedCallsAreMarkedWhenTheRunEnds(t *testing.T) {
	m := testModel(t)
	announceCalls(m, 0, ds4.ToolCall{ID: "c1", Name: "trip_preview", Arguments: `{}`})
	// A round-limit stop ends the run without executing the announced call.
	m.Update(doneMsg{})
	text := m.activityText()
	if !strings.Contains(text, "not run") || strings.Contains(text, "running") {
		t.Fatalf("unexecuted call not marked at end of run:\n%s", text)
	}
}

func TestToolDetailsAreBoundedAndSanitized(t *testing.T) {
	m := testModel(t)
	announceCalls(m, 0, ds4.ToolCall{ID: "c1", Name: "trip_set_shader", Arguments: strings.Repeat("界", 5000) + "\x1b[2J"})
	returnResults(m, ds4.ChatMessage{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("x", 100000)})
	text := m.activityText()
	if strings.Contains(text, "\x1b") {
		t.Fatal("terminal control sequence reached the inspector")
	}
	if len(text) > 8000 {
		t.Fatalf("one call produced %d bytes of inspector text", len(text))
	}
}

func TestResultWithoutMatchingCallIsIgnored(t *testing.T) {
	m := testModel(t)
	announceCalls(m, 0, ds4.ToolCall{ID: "c1", Name: "trip_describe", Arguments: `{}`})
	returnResults(m, ds4.ChatMessage{Role: "tool", ToolCallID: "unknown", Content: "ERROR: stray"})
	if text := m.activityText(); strings.Contains(text, "failed") {
		t.Fatalf("a stray result was counted against a call:\n%s", text)
	}
}

// pane builds a model with a log and three calls: one done in 12ms, one that
// failed, and one still running.
func panedModel(t *testing.T) *model {
	t.Helper()
	m := testModel(t)
	announceCalls(m, 0,
		ds4.ToolCall{ID: "a", Name: "trip_set_param", Arguments: `{}`},
		ds4.ToolCall{ID: "b", Name: "trip_set_shader", Arguments: `{}`},
		ds4.ToolCall{ID: "c", Name: "trip_preview", Arguments: `{}`},
	)
	returnResults(m,
		ds4.ChatMessage{Role: "tool", ToolCallID: "a", Content: "ok"},
		ds4.ChatMessage{Role: "tool", ToolCallID: "b", Content: "ERROR: 2:8: unknown identifier 'uvv'\nsecond line"},
	)
	m.activity[0].calls[0].took = 12 * time.Millisecond
	// Set after the events, which log their results themselves.
	m.log = []string{"[tool] trip_set_param", "[tool] trip_set_shader", "result noise"}
	return m
}

func stripLines(body string) []string { return strings.Split(ansi.Strip(body), "\n") }

func TestToolPaneSeparatesLogFromCalls(t *testing.T) {
	m := panedModel(t)
	lines := stripLines(m.toolPaneBody(30, 9))
	div := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "─ calls") {
			div = i
		}
	}
	if div < 1 {
		t.Fatalf("no divider below the log:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines[:div], "\n"), "result noise") {
		t.Fatalf("log not above the divider:\n%s", strings.Join(lines, "\n"))
	}
	below := strings.Join(lines[div+1:], "\n")
	for _, want := range []string{"✓ trip_set_param", "✗ trip_set_shader", "… trip_preview"} {
		if !strings.Contains(below, want) {
			t.Fatalf("%q not below the divider:\n%s", want, strings.Join(lines, "\n"))
		}
	}
	if strings.Contains(strings.Join(lines[:div], "\n"), "trip_preview") && !strings.Contains(lines[0], "[tool]") {
		t.Fatalf("a call leaked into the log section:\n%s", strings.Join(lines, "\n"))
	}
	if len(lines) > 9 {
		t.Fatalf("body is %d lines, taller than the pane (9)", len(lines))
	}
}

func TestToolPaneShowsDurationsAlignedAndFailureReason(t *testing.T) {
	m := panedModel(t)
	lines := stripLines(m.toolPaneBody(30, 9))
	var done, failed, reason string
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "✓ trip_set_param"):
			done = l
		case strings.HasPrefix(l, "✗ trip_set_shader"):
			failed = l
			if i+1 < len(lines) {
				reason = lines[i+1]
			}
		}
	}
	if !strings.HasSuffix(done, "12ms") || lipgloss.Width(done) != 30 {
		t.Fatalf("duration not right-aligned to the pane edge: %q (width %d)", done, lipgloss.Width(done))
	}
	if strings.Contains(failed, "ERROR") {
		t.Fatalf("failed call line carries the raw error: %q", failed)
	}
	if !strings.Contains(reason, "2:8: unknown identifier") || strings.Contains(reason, "ERROR") || strings.Contains(reason, "second line") {
		t.Fatalf("failure reason should be the first error line without its prefix: %q", reason)
	}
}

func TestToolPaneStatusColorsDiffer(t *testing.T) {
	m := panedModel(t)
	var okPrefix, failPrefix, runPrefix string
	for _, l := range strings.Split(m.toolPaneBody(30, 9), "\n") {
		plain := ansi.Strip(l)
		switch {
		case strings.HasPrefix(plain, "✓"):
			okPrefix = l[:strings.Index(l, "✓")]
		case strings.HasPrefix(plain, "✗"):
			failPrefix = l[:strings.Index(l, "✗")]
		case strings.HasPrefix(plain, "…"):
			runPrefix = l[:strings.Index(l, "…")]
		}
	}
	if okPrefix == "" || failPrefix == "" || runPrefix == "" {
		t.Fatalf("status marks are not styled: ok=%q fail=%q run=%q", okPrefix, failPrefix, runPrefix)
	}
	if okPrefix == failPrefix || failPrefix == runPrefix || okPrefix == runPrefix {
		t.Fatalf("statuses share a style: ok=%q fail=%q run=%q", okPrefix, failPrefix, runPrefix)
	}
}

func TestToolPaneDropsLogAndDividerWhenShort(t *testing.T) {
	m := panedModel(t)
	if got := stripLines(m.toolPaneBody(30, 1)); len(got) != 1 || !strings.HasPrefix(got[0], "… trip_preview") {
		t.Fatalf("one row should hold only the newest call: %q", got)
	}
	// Four rows: three calls plus the failure's reason leave no room for a log.
	for _, l := range stripLines(m.toolPaneBody(30, 4)) {
		if strings.HasPrefix(l, "─") || strings.Contains(l, "[tool]") {
			t.Fatalf("log or divider squeezed into a pane with no room: %q", l)
		}
	}
}

func TestToolPaneLinesNeverExceedWidth(t *testing.T) {
	m := panedModel(t)
	m.activity[0].calls[1].name = strings.Repeat("very_long_tool_name_", 5)
	m.activity[0].calls[1].result = "ERROR: " + strings.Repeat("界", 80)
	for _, w := range []int{12, 18, 30} {
		for i, l := range strings.Split(m.toolPaneBody(w, 9), "\n") {
			if lipgloss.Width(l) > w {
				t.Fatalf("width %d: line %d is %d cells: %q", w, i, lipgloss.Width(l), ansi.Strip(l))
			}
		}
	}
}

func TestToolPaneWithoutCallsKeepsLogAndHint(t *testing.T) {
	m := testModel(t)
	if got := ansi.Strip(m.toolPaneBody(30, 3)); !strings.Contains(got, "No tools yet") {
		t.Fatalf("empty pane lost its hint: %q", got)
	}
	m.log = []string{"hello log"}
	if got := ansi.Strip(m.toolPaneBody(30, 3)); !strings.Contains(got, "hello log") || strings.Contains(got, "─ calls") {
		t.Fatalf("log-only pane wrong: %q", got)
	}
}

func TestToolPaneShowsStreamedNamesAsRunning(t *testing.T) {
	m := testModel(t)
	m.startActivityRound(0)
	m.activity[0].tools = []string{"trip_set_shader"}
	if got := ansi.Strip(m.toolPaneBody(30, 3)); !strings.Contains(got, "… trip_set_shader") {
		t.Fatalf("a call seen only in the stream is not shown as running: %q", got)
	}
}
