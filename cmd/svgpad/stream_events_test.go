package main

import (
	"log"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go/dsml"
	svg "github.com/NimbleMarkets/ntcharts-svg/svg"
)

func testModel() model {
	return model{
		toolCallCounts: make(map[string]int),
		maxToolRounds:  10,
		maxAutoCorrect: 3,
		logger:         log.New(os.Stderr, "", 0),
		svgWidget:      svg.New(8, 4),
	}
}

func update(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	nm, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return nm
}

func TestStreamEventsDriveThinkingAndToolPanels(t *testing.T) {
	m := testModel()
	m.generating = true

	m = update(t, m, bubble.StreamEventMsg{Event: dsml.StreamEvent{Type: dsml.EventReasoningDelta, Delta: "plan the scene"}})
	m = update(t, m, bubble.StreamEventMsg{Event: dsml.StreamEvent{Type: dsml.EventContentDelta, Delta: "Here is the drawing."}})
	if m.thinkText != "plan the scene" {
		t.Errorf("thinkText = %q", m.thinkText)
	}
	if m.outputText != "Here is the drawing." {
		t.Errorf("outputText = %q", m.outputText)
	}

	m = update(t, m, bubble.StreamEventMsg{Event: dsml.StreamEvent{Type: dsml.EventToolCallStart, Index: 0, Name: "svg_append"}})
	m = update(t, m, bubble.StreamEventMsg{Event: dsml.StreamEvent{Type: dsml.EventToolCallArgumentsDelta, Index: 0, Delta: `{"chunk": "<rect/>"`}})
	m = update(t, m, bubble.StreamEventMsg{Event: dsml.StreamEvent{Type: dsml.EventToolCallEnd, Index: 0, Arguments: `{"chunk": "<rect/>"}`}})

	if len(m.toolCalls) != 1 {
		t.Fatalf("toolCalls = %d entries, want 1", len(m.toolCalls))
	}
	if m.toolCalls[0].name != "svg_append" || !strings.Contains(m.toolCalls[0].args, "<rect/>") {
		t.Errorf("entry = %+v", m.toolCalls[0])
	}
	if m.toolCalls[0].round != 1 {
		t.Errorf("entry round = %d, want 1 (first tool round)", m.toolCalls[0].round)
	}
}

func TestToolResultsFillEntriesAndCounters(t *testing.T) {
	m := testModel()
	m.generating = true

	m = update(t, m, roundStartedMsg{round: 0})
	m = update(t, m, bubble.StreamEventMsg{Event: dsml.StreamEvent{Type: dsml.EventToolCallStart, Index: 0, Name: "svg_append"}})
	m = update(t, m, bubble.StreamEventMsg{Event: dsml.StreamEvent{Type: dsml.EventToolCallEnd, Index: 0, Arguments: `{"chunk":"<rect/>"}`}})
	m = update(t, m, toolCallsMsg{calls: []ds4.ToolCall{{ID: "c1", Name: "svg_append", Arguments: `{"chunk":"<rect/>"}`}}})
	if !strings.Contains(m.statusText, "svg_append") {
		t.Errorf("statusText = %q, want running tool names", m.statusText)
	}
	m = update(t, m, toolResultsMsg{results: []ds4.ChatMessage{{Role: "tool", ToolCallID: "c1", Content: "Chunk appended successfully."}}})

	if m.toolRounds != 1 || m.totalToolRounds != 1 || m.totalToolCalls != 1 {
		t.Errorf("counters: rounds=%d totalRounds=%d totalCalls=%d", m.toolRounds, m.totalToolRounds, m.totalToolCalls)
	}
	if m.toolCallCounts["svg_append"] != 1 {
		t.Errorf("toolCallCounts = %v", m.toolCallCounts)
	}
	if len(m.toolCalls) != 1 || !strings.Contains(m.toolCalls[0].result, "appended") {
		t.Fatalf("toolCalls = %+v", m.toolCalls)
	}
}

// A truncation-repaired block executes without ever streaming tool events;
// the results handler must create the missing panel entries.
func TestToolResultsReconcileMissingEntries(t *testing.T) {
	m := testModel()
	m.generating = true

	m = update(t, m, roundStartedMsg{round: 0})
	m = update(t, m, toolCallsMsg{calls: []ds4.ToolCall{{ID: "c1", Name: "svg_validate", Arguments: `{}`}}})
	m = update(t, m, toolResultsMsg{results: []ds4.ChatMessage{{Role: "tool", ToolCallID: "c1", Content: "Valid: ok"}}})

	if len(m.toolCalls) != 1 || m.toolCalls[0].name != "svg_validate" {
		t.Fatalf("toolCalls = %+v, want reconciled svg_validate entry", m.toolCalls)
	}
	if !strings.Contains(m.toolCalls[0].result, "Valid") {
		t.Errorf("result = %q", m.toolCalls[0].result)
	}
}

func TestAppendChunkUpdatesLivePreview(t *testing.T) {
	m := testModel()
	m.generating = true
	m.previewBase = `<svg xmlns="http://www.w3.org/2000/svg">`

	m = update(t, m, bubble.StreamEventMsg{Event: dsml.StreamEvent{Type: dsml.EventToolCallStart, Index: 0, Name: "svg_append"}})
	m = update(t, m, bubble.StreamEventMsg{Event: dsml.StreamEvent{Type: dsml.EventToolCallEnd, Index: 0, Arguments: `{"chunk":"<rect width=\"4\"/>"}`}})

	if len(m.previewPending) != 1 || m.previewPending[0] != `<rect width="4"/>` {
		t.Fatalf("previewPending = %q", m.previewPending)
	}
	want := m.previewBase + `<rect width="4"/>` + "</svg>"
	if got := previewSVG(m.previewBase, m.previewPending); string(got) != want {
		t.Errorf("preview = %q, want %q", got, want)
	}
}

func TestRoundStartedResetsPanelsAndStreamState(t *testing.T) {
	m := testModel()
	m.generating = true
	m.thinkText = "old"
	m.outputText = "old"
	m.toolRounds = 2

	m = update(t, m, roundStartedMsg{round: 1})
	if m.thinkText != "" || m.outputText != "" {
		t.Errorf("panels not reset: think=%q out=%q", m.thinkText, m.outputText)
	}
	if m.statusText != "Generate [2/10]..." {
		t.Errorf("statusText = %q, want roundStatus()", m.statusText)
	}
}

func TestMalformedRetrySurfacesStatus(t *testing.T) {
	m := testModel()
	m.generating = true
	m = update(t, m, malformedRetryMsg{reason: "invoke missing name"})
	if !strings.Contains(m.statusText, "tool syntax") {
		t.Errorf("statusText = %q, want a tool-syntax retry note", m.statusText)
	}
}

// Regression: a turn that hits the driver's round cap (ErrMaxRounds) with an
// invalid or missing SVG must still enter the auto-correct gate — the cap is
// a completed-turn condition, not a generation failure. (Previously the gate
// keyed on msg.err == nil and silently saved the broken draft.)
func TestMaxRoundsStillEntersAutoCorrectGate(t *testing.T) {
	m := testModel()
	m.generating = true
	m.workDir = t.TempDir() // no draft.svg -> no SVG markup found
	m.history = []ds4.ChatMessage{{Role: "user", Content: "draw"}}

	m = update(t, m, turnDoneMsg{
		result: bubble.RunResult{
			Assistant: ds4.ChatMessage{Role: "assistant", ToolCalls: []ds4.ToolCall{{Name: "svg_append"}}},
			History:   []ds4.ChatMessage{{Role: "user", Content: "draw"}},
		},
		err: bubble.ErrMaxRounds,
	})

	if m.autoCorrectCount != 1 {
		t.Fatalf("autoCorrectCount = %d, want 1 (gate must run after ErrMaxRounds)", m.autoCorrectCount)
	}
	if !m.generating {
		t.Error("generating = false, want a correction turn in flight")
	}
	if !strings.Contains(m.statusText, "Fixing SVG (1/3)") {
		t.Errorf("statusText = %q, want Fixing SVG (1/3)", m.statusText)
	}
	last := m.history[len(m.history)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "invalid") {
		t.Errorf("missing auto-correct feedback turn, last = %+v", last)
	}
}
