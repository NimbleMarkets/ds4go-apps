package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestToolRoundConfiguration(t *testing.T) {
	for _, rounds := range []int{1, 20, 30} {
		if err := validateToolRounds(rounds); err != nil {
			t.Errorf("%d: %v", rounds, err)
		}
	}
	for _, rounds := range []int{-1, 0, math.MaxInt} {
		if err := validateToolRounds(rounds); err == nil {
			t.Errorf("accepted %d", rounds)
		}
	}
	if defaultToolRounds != 20 {
		t.Fatalf("default=%d", defaultToolRounds)
	}
	m := model{maxToolRounds: 30, visual: visualOptions{Mode: "on", MaxPasses: 4}}
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(ds4.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	m.engine = eng
	for _, want := range []string{"30 tool-capable rounds", "up to 4 passes", "Vision feedback is available"} {
		if !strings.Contains(m.systemPrompt(), want) {
			t.Errorf("runtime prompt missing %q", want)
		}
	}
	m.visual.Mode = "off"
	if !strings.Contains(m.systemPrompt(), "Vision feedback is unavailable") {
		t.Fatal("off mode still advertises vision")
	}
	ctl.SetVision(false)
	m.visual.Mode = "auto"
	if !strings.Contains(m.systemPrompt(), "Vision feedback is unavailable") {
		t.Fatal("text engine still advertises vision")
	}
}

func TestSVGBudgetGuidance(t *testing.T) {
	initial := []ds4.ChatMessage{{Role: "user", Content: "draw a pelican"}}
	for _, tc := range []struct {
		round int
		want  string
	}{
		{0, "20 of 20"}, {17, "3 of 20"}, {19, "1 of 20"}, {20, "0 of 20"},
	} {
		history := prepareSVGHistory(initial, tc.round, 21)
		msg := history[len(history)-1]
		if msg.Role != "user" || msg.ToolCallID != toolBudgetFeedbackID || !strings.Contains(msg.Content, tc.want) {
			t.Fatalf("round %d: %+v", tc.round, msg)
		}
		if tc.round == 17 && !strings.Contains(msg.Content, "Finish up") {
			t.Fatal("no end-of-budget warning")
		}
		if tc.round == 20 && !strings.Contains(msg.Content, "without tool calls") {
			t.Fatal("final turn not reserved for answer")
		}
		if len(initial) != 1 || initial[0].Content != "draw a pelican" {
			t.Fatal("original history changed")
		}
	}
	// A review/retry starts a fresh budget, regardless of the prior phase's count.
	history := prepareSVGHistory(initial, 20, 21)
	history = prepareSVGHistory(history, 0, 21)
	if !strings.Contains(history[len(history)-1].Content, "fresh budget") {
		t.Fatal("review phase did not reset guidance")
	}
	// Selecting the original user prompt for metadata must skip these host notes.
	var prompt string
	for _, msg := range history {
		if msg.Role == "user" && msg.ToolCallID == "" {
			prompt = msg.Content
		}
	}
	if prompt != "draw a pelican" {
		t.Fatalf("metadata prompt=%q", prompt)
	}
}

func TestSVGToolBudgetEnforcementAndGuidance(t *testing.T) {
	for _, tc := range []struct {
		name         string
		limit, calls int
		wantCap      bool
	}{
		{"past old cap", defaultToolRounds, 11, false},
		{"final answer at cap", defaultToolRounds, 20, false},
		{"pending tools at cap", 2, 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := ds4api.NewMockLibrary()
			eng, err := lib.NewEngine(ds4.EngineOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer eng.Close()
			sess, err := eng.NewSession(8192)
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close()
			reg := ds4.NewToolRegistry()
			executed := 0
			reg.MustRegister(ds4.Tool{ToolSchema: ds4.ToolSchema{Name: "check", Parameters: json.RawMessage(`{"type":"object"}`)}, Handler: func(context.Context, json.RawMessage) (string, error) { executed++; return "checked", nil }})
			block, err := dsml.RenderToolCalls([]dsml.ToolCall{{Name: "check", Arguments: `{}`}})
			if err != nil {
				t.Fatal(err)
			}
			round := 0
			d := bubble.NewGenerationDriver(bubble.DriverOptions{
				Engine: eng, Session: sess, Tools: reg, MaxRounds: tc.limit + 1, ExecuteTools: reg.ExecuteToolCalls,
				PrepareHistory: func(h []ds4.ChatMessage, r, maxRounds int) []ds4.ChatMessage {
					if r != round || maxRounds != tc.limit+1 {
						t.Fatalf("round=%d max=%d, expected %d/%d", r, maxRounds, round, tc.limit+1)
					}
					next := prepareSVGHistory(h, r, maxRounds)
					if !strings.Contains(next[len(next)-1].Content, fmt.Sprintf("%d of %d", tc.limit-r, tc.limit)) {
						t.Fatal("incorrect remaining count")
					}
					return next
				},
				CompletePrompt: func(_ *ds4.Prompt, _ ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
					round++
					if round <= tc.calls {
						return block, nil
					}
					return "Finished.", nil
				},
			})
			res, err := d.RunWithPrompt(context.Background(), "Draw and finish within budget.", []ds4.ChatMessage{{Role: "user", Content: "draw"}})
			if errors.Is(err, bubble.ErrMaxRounds) != tc.wantCap || (err != nil && !tc.wantCap) {
				t.Fatalf("error=%v", err)
			}
			if executed != min(tc.calls, tc.limit) || res.ToolRounds != executed {
				t.Fatalf("executed=%d reported=%d", executed, res.ToolRounds)
			}
			if !tc.wantCap && res.Assistant.Content != "Finished." {
				t.Fatal("final answer was cut off")
			}
			notices := 0
			for _, msg := range res.History {
				if msg.ToolCallID == toolBudgetFeedbackID {
					notices++
				}
			}
			if notices != round {
				t.Fatalf("budget notices=%d model rounds=%d", notices, round)
			}
		})
	}
}
