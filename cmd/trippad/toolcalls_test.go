package main

import (
	"strings"
	"testing"

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
