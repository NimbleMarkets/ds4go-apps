package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestSVGContextGuidance(t *testing.T) {
	for _, tc := range []struct {
		used int
		want string
	}{
		{4000, "Keep reasoning"}, {24576, "running low"}, {30000, "critically low"},
	} {
		note := svgContextFeedback(bubble.ContextUsageEvent{PromptTokens: tc.used, Capacity: 32768})
		if !strings.Contains(note, tc.want) || !strings.Contains(note, "Tool-round budgets do not reset") {
			t.Fatalf("used=%d note=%s", tc.used, note)
		}
	}
}

func TestSVGContextCheckpointPreservesRequestsAndDraft(t *testing.T) {
	draft := filepath.Join(t.TempDir(), "draft.svg")
	data := []byte(visualFixture)
	if err := os.WriteFile(draft, data, 0644); err != nil {
		t.Fatal(err)
	}
	history := []ds4.ChatMessage{
		{Role: "user", Content: "Draw a pelican, use blue labels"},
		{Role: "assistant", ReasoningContent: "old reasoning", ToolCalls: []ds4.ToolCall{{ID: "1", Name: "svg_append", Arguments: "old markup"}}},
		{Role: "tool", ToolCallID: "1", Content: "saved"},
		{Role: "user", ToolCallID: toolBudgetFeedbackID, Content: "old budget"},
		{Role: "user", ToolCallID: bubble.ContextFeedbackID, Content: "old capacity"},
		{Role: "user", ToolCallID: svgCorrectionID, Content: "obsolete syntax correction"},
		{Role: "user", ToolCallID: visualFeedbackID, Parts: []ds4.ContentPart{{Image: &ds4.ImageInput{Data: []byte("old image")}}}},
		{Role: "user", Content: "Make the labels larger"},
		{Role: "assistant", ToolCalls: []ds4.ToolCall{{ID: "2", Name: "svg_replace", Arguments: "not executed"}}},
	}
	next, err := checkpointSVGHistory(history, draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 3 || !reflect.DeepEqual(next[:2], []ds4.ChatMessage{history[0], history[7]}) {
		t.Fatalf("requests not retained: %+v", next)
	}
	if next[2].ToolCallID != svgCheckpointID || !strings.Contains(next[2].Content, "first use svg_read") {
		t.Fatalf("missing recovery guidance: %+v", next[2])
	}
	if len(history) != 9 || len(history[6].Parts) != 1 {
		t.Fatal("mutated source history")
	}
	after, err := os.ReadFile(draft)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("draft changed")
	}
	// Repeated recovery replaces the previous checkpoint instead of accumulating it.
	again, err := checkpointSVGHistory(next, draft)
	if err != nil || !reflect.DeepEqual(again, next) {
		t.Fatalf("checkpoint not stable: %v", err)
	}
	missing, err := checkpointSVGHistory(history, filepath.Join(t.TempDir(), "missing.svg"))
	if err != nil || !strings.Contains(missing[len(missing)-1].Content, "no saved draft content") {
		t.Fatalf("missing draft: %v", err)
	}
	if _, err := checkpointSVGHistory(history, t.TempDir()); err == nil {
		t.Fatal("ignored draft read failure")
	}
}

func TestContinueAfterContextLimitCheckpointsBeforeStarting(t *testing.T) {
	for _, failure := range []error{ds4.ErrContextFull, bubble.ErrContextBudget} {
		m := testModel()
		m.workDir = t.TempDir()
		m.lastErr = failure
		m.history = []ds4.ChatMessage{{Role: "user", Content: "draw"}, {Role: "assistant", Content: "obsolete answer"}}
		m.toolCalls = []toolCallEntry{{name: "svg_append"}}
		m.liveCalls = []int{0}
		m.pendingCalls = []ds4.ToolCall{{Name: "svg_append"}}
		if err := os.WriteFile(filepath.Join(m.workDir, "draft.svg"), []byte(visualFixture), 0644); err != nil {
			t.Fatal(err)
		}
		m = update(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
		if !m.generating || len(m.history) != 2 || m.history[1].ToolCallID != svgCheckpointID {
			t.Fatal("continue did not checkpoint")
		}
		if len(m.liveCalls) != 0 || len(m.pendingCalls) != 0 || m.roundCallStart != len(m.toolCalls) {
			t.Fatal("stale stream indices after recovery")
		}
		// This test deliberately has no engine; drain the runner's terminal error.
		if _, ok := m.gen.Wait()().(turnDoneMsg); !ok {
			t.Fatal("runner did not finish")
		}
		m.gen.Cancel()
	}
}

func TestContextUsageUpdatesUI(t *testing.T) {
	m := testModel()
	m = update(t, m, bubble.ContextUsageEvent{PromptTokens: 24000, Capacity: 32768})
	if m.ctxPos != 24000 || m.ctxSize != 32768 {
		t.Fatal("context meter not updated")
	}
}

func TestCheckpointResyncFreesSessionContext(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	eng, err := lib.NewEngine(ds4.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(4096)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	old := []ds4.ChatMessage{{Role: "user", Content: "draw a pelican"}, {Role: "assistant", Content: strings.Repeat("old observations ", 1800)}}
	reg := ds4.NewToolRegistry()
	p, err := reg.BuildPromptMultimodal(eng, nil, "sys", old, ds4.ThinkNone)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Sync(p.Tokens.Slice()); err != nil {
		t.Fatal(err)
	}
	p.Free()
	before := sess.Pos()
	if sess.Ctx()-before > svgMinResponseTokens {
		t.Fatal("fixture session not full enough")
	}
	history, err := checkpointSVGHistory(old, filepath.Join(t.TempDir(), "draft.svg"))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	d := bubble.NewGenerationDriver(bubble.DriverOptions{
		Engine: eng, Session: sess, Tools: reg, ThinkMode: ds4.ThinkNone,
		PrepareHistory: prepareSVGHistory, ContextFeedback: svgContextFeedback, MinResponseTokens: svgMinResponseTokens,
		CompletePrompt: func(p *ds4.Prompt, _ ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
			called = true
			if err := sess.Sync(p.Tokens.Slice()); err != nil {
				return "", err
			}
			if sess.Pos() >= before {
				t.Fatal("checkpoint did not free session context")
			}
			return "Done", nil
		},
	})
	if _, err := d.RunWithPrompt(context.Background(), "sys", history); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("old session position incorrectly prevented recovery")
	}
}
