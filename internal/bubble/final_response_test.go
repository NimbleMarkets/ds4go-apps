package bubble

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestFinalResponseHidesSchemasAndRejectsFurtherCalls(t *testing.T) {
	for _, obey := range []bool{true, false} {
		t.Run(map[bool]string{true: "summary", false: "ignored-limit"}[obey], func(t *testing.T) {
			eng, sess, reg := mockDriverEnv(t)
			if err := reg.RegisterFunc(ds4.ToolSchema{Name: "unused", Description: "before UNIQUE_SCHEMA_SENTINEL after", Parameters: json.RawMessage(`{"type":"object"}`)}, nil); err != nil {
				t.Fatal(err)
			}
			marker, err := eng.TokenizeText("UNIQUE_SCHEMA_SENTINEL")
			if err != nil {
				t.Fatal(err)
			}
			defer marker.Free()
			block, err := dsml.RenderToolCalls([]dsml.ToolCall{{Name: "svg_append", Arguments: `{}`}})
			if err != nil {
				t.Fatal(err)
			}
			generated, executed := 0, 0
			var rejected []ds4.ChatMessage
			d := NewGenerationDriver(DriverOptions{
				Engine: eng, Session: sess, Tools: reg, MaxRounds: 2, FinalResponseOnly: true,
				ContextFeedback: func(ContextUsageEvent) string { return "Budget feedback triggers a prompt rebuild." },
				CompleteTurn: func(p *ds4.Tokens, _ ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
					generated++
					if got := slices.Contains(p.Slice(), marker.Slice()[0]); got != (generated == 1) {
						t.Fatalf("round %d advertised schemas: %v", generated, got)
					}
					if generated == 2 && obey {
						return "Finished drawing; lighting remains unfinished.", nil
					}
					return block, nil
				},
				ExecuteTools: func(ctx context.Context, c []ds4.ToolCall) ([]ds4.ChatMessage, error) {
					executed++
					return reg.ExecuteToolCalls(ctx, c)
				},
				OnEvent: func(ev Event) {
					if e, ok := ev.(ToolResultsEvent); ok {
						for _, r := range e.Results {
							if strings.HasPrefix(r.Content, "NOT EXECUTED:") {
								rejected = append(rejected, r)
							}
						}
					}
				},
			})
			res, err := d.RunWithPrompt(context.Background(), "draw", []ds4.ChatMessage{{Role: "user", Content: "make a scene"}})
			if generated != 2 || executed != 1 || res.ToolRounds != 1 || !res.BudgetExhausted {
				t.Fatalf("budget violated: gen=%d exec=%d result=%+v", generated, executed, res)
			}
			if obey {
				if err != nil || len(rejected) != 0 || !strings.Contains(res.Assistant.Content, "unfinished") {
					t.Fatalf("lost summary: %+v %v", res, err)
				}
			} else {
				if !errors.Is(err, ErrMaxRounds) || len(rejected) != 1 {
					t.Fatalf("missing rejection: %+v %v", rejected, err)
				}
				last := res.History[len(res.History)-1]
				if last.Role != "tool" || last.ToolCallID != res.Assistant.ToolCalls[0].ID || last.Content != rejected[0].Content {
					t.Fatal("dangling rejected call in history")
				}
			}
			// A follow-up must render the retained history without replaying work.
			p, err := reg.BuildPrompt(eng, "continue", append(res.History, ds4.ChatMessage{Role: "user", Content: "continue"}), ds4.ThinkNone)
			if err != nil {
				t.Fatal(err)
			}
			p.Free()
			if executed != 1 {
				t.Fatal("history replay executed a call")
			}
		})
	}
}
