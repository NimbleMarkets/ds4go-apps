package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/memory"
)

func TestRoundStopSavesCheckpointAndShowsHonestSummary(t *testing.T) {
	m := testModel(t)
	mem, err := memory.Open(t.TempDir(), "run")
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	before := m.state.Snapshot()
	request := "Make a sponge; keep the background blue."
	res := bubble.RunResult{ToolRounds: 20, BudgetExhausted: true,
		Assistant: ds4.ChatMessage{Role: "assistant", Content: "Successfully saved everything!", ToolCalls: []ds4.ToolCall{{ID: "pending", Name: "trip_save_preset"}}}}
	res.History = []ds4.ChatMessage{{Role: "user", Content: request}, {Role: "tool", ToolCallID: "ok", Content: "compile OK: sponge"}, res.Assistant, {Role: "tool", ToolCallID: "pending", Content: "NOT EXECUTED: trip_save_preset"}}
	msg := finishRun(context.Background(), res, bubble.ErrMaxRounds, m.state, mem)
	if msg.err != nil || len(msg.result.Assistant.ToolCalls) != 0 || strings.Contains(msg.result.Assistant.Content, "Successfully saved") || !strings.Contains(msg.result.Assistant.Content, "Not executed: trip_save_preset") {
		t.Fatalf("bad summary: %+v", msg)
	}
	if !strings.Contains(msg.notice, "checkpoint saved") || !reflect.DeepEqual(before, m.state.Snapshot()) {
		t.Fatal("checkpoint changed shader or was not saved")
	}
	note, err := mem.Call(context.Background(), "session", "get", []byte(`{"key":"checkpoint"}`))
	if err != nil || !strings.Contains(note, request) || !strings.Contains(note, "compile OK") || !strings.Contains(note, "NOT EXECUTED") {
		t.Fatalf("incomplete checkpoint: %s %v", note, err)
	}
	brief, err := mem.Brief(context.Background())
	if err != nil || !strings.Contains(brief, "last-result") || !strings.Contains(brief, "Round limit reached") {
		t.Fatal("next request will not see the stop reason")
	}
	m.Update(msg)
	if !strings.Contains(m.status, "checkpoint saved") || !strings.Contains(m.activityText(), "Not executed") {
		t.Fatal("TUI hid final summary/stop status")
	}
	if res.Assistant.Content != "Successfully saved everything!" {
		t.Fatal("mutated input result")
	}
}

func TestRoundStopRetainsFinalAnswerAndReportsCheckpointFailure(t *testing.T) {
	m := testModel(t)
	mem, err := memory.Open(t.TempDir(), "run")
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	res := bubble.RunResult{BudgetExhausted: true, ToolRounds: 2, Assistant: ds4.ChatMessage{Role: "assistant", Content: "Geometry finished; color remains."}}
	res.History = []ds4.ChatMessage{{Role: "user", Content: "Make geometry"}, res.Assistant}
	msg := finishRun(context.Background(), res, nil, m.state, mem)
	if msg.err != nil || !reflect.DeepEqual(msg.result.History, res.History) || msg.result.Assistant.Content != res.Assistant.Content {
		t.Fatal("lost successful final answer")
	}
	// A non-file checkpoint simulates a write failure without changing permissions.
	path := filepath.Join(mem.Dir, "sessions", mem.Session, "checkpoint")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	msg = finishRun(context.Background(), res, nil, m.state, mem)
	if msg.err == nil || strings.Contains(msg.notice, "checkpoint saved") || !reflect.DeepEqual(msg.result.History, res.History) {
		t.Fatal("checkpoint failure hidden or history lost")
	}
}

func TestRoundStopHandlesFailedSummaryAndCancellation(t *testing.T) {
	m := testModel(t)
	for _, err := range []error{bubble.ErrContextBudget, context.Canceled} {
		res := bubble.RunResult{BudgetExhausted: true, ToolRounds: 1, History: []ds4.ChatMessage{{Role: "user", Content: "keep going"}}}
		msg := finishRun(context.Background(), res, err, m.state, nil)
		if !errors.Is(msg.err, err) || !strings.Contains(msg.result.Assistant.Content, "Final summary could not complete") {
			t.Fatalf("failure hidden: %+v", msg)
		}
	}
}
