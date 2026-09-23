package bubble

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestCompactionRebuildsPromptAndKeepsRoundBudget(t *testing.T) {
	eng, _, reg := mockDriverEnv(t)
	sess, err := eng.NewSession(2200)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	block, err := dsml.RenderToolCalls([]dsml.ToolCall{{Name: "svg_append", Arguments: `{}`}})
	if err != nil {
		t.Fatal(err)
	}
	var usage ContextUsageEvent
	var compacted ContextCompactedEvent
	generated, executed, checks := 0, 0, 0
	d := NewGenerationDriver(DriverOptions{
		Engine: eng, Session: sess, Tools: reg, ThinkMode: ds4.ThinkNone, MaxRounds: 2, MaxTokens: 4096, MinResponseTokens: 512, ResponseReserveTokens: 64,
		CompactHistory: func(_ context.Context, h []ds4.ChatMessage, u ContextUsageEvent) ([]ds4.ChatMessage, bool, error) {
			checks++
			if checks == 1 {
				if u.PromptTokens < 2200 {
					t.Fatal("did not measure oversized prompt")
				}
				return append([]ds4.ChatMessage(nil), h[1:]...), true, nil
			}
			return h, false, nil
		},
		ContextFeedback: func(ContextUsageEvent) string { return "Count this feedback too." },
		OnEvent: func(e Event) {
			switch e := e.(type) {
			case ContextUsageEvent:
				usage = e
			case ContextCompactedEvent:
				compacted = e
			}
		},
		CompletePrompt: func(p *ds4.Prompt, o ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
			generated++
			if p.Tokens.Len() != usage.PromptTokens || o.MaxTokens > usage.Remaining()-64 || o.MaxTokens <= 0 {
				t.Fatalf("unmeasured budget: %+v max=%d prompt=%d", usage, o.MaxTokens, p.Tokens.Len())
			}
			return block, nil
		},
		ExecuteTools: func(ctx context.Context, c []ds4.ToolCall) ([]ds4.ChatMessage, error) {
			executed++
			return reg.ExecuteToolCalls(ctx, c)
		},
	})
	history := []ds4.ChatMessage{{Role: "assistant", Content: strings.Repeat("old context ", 1300)}, {Role: "user", Content: "keep the active request"}}
	res, err := d.RunWithPrompt(context.Background(), "sys", history)
	if !errors.Is(err, ErrMaxRounds) || generated != 2 || executed != 1 || checks != 2 || res.ToolRounds != 1 {
		t.Fatalf("round budget changed: gen=%d exec=%d checks=%d rounds=%d err=%v", generated, executed, checks, res.ToolRounds, err)
	}
	if compacted.After >= compacted.Before || res.History[0].Content != "keep the active request" {
		t.Fatalf("bad compaction: %+v", compacted)
	}
	if len(history[0].Content) < 2500 {
		t.Fatal("mutated input")
	}
}

func TestCompactionFailureKeepsHistoryWithoutGeneration(t *testing.T) {
	eng, sess, reg := mockDriverEnv(t)
	want := errors.New("checkpoint disk full")
	h := []ds4.ChatMessage{{Role: "user", Content: "preserve this"}}
	d := NewGenerationDriver(DriverOptions{Engine: eng, Session: sess, Tools: reg,
		CompactHistory: func(context.Context, []ds4.ChatMessage, ContextUsageEvent) ([]ds4.ChatMessage, bool, error) {
			return nil, false, want
		},
		CompleteTurn: func(*ds4.Tokens, ds4.GenerateOptions, func(dsml.StreamEvent)) (string, error) {
			t.Fatal("generated after checkpoint failure")
			return "", nil
		},
	})
	res, err := d.RunWithPrompt(context.Background(), "sys", h)
	if !errors.Is(err, want) || len(res.History) != 1 || res.History[0].Content != "preserve this" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestContextPreflightCountsFeedbackAndProtectsGeneration(t *testing.T) {
	eng, _, reg := mockDriverEnv(t)
	history := []ds4.ChatMessage{
		{Role: "user", Content: "draw"},
		{Role: "assistant", Content: "previous drawing"},
		{Role: "user", Content: "revise the drawing"},
	}
	p, err := reg.BuildPromptMultimodal(eng, nil, "sys", history, ds4.ThinkNone)
	if err != nil {
		t.Fatal(err)
	}
	initial := p.Tokens.Len()
	p.Free()
	for _, tc := range []struct {
		name     string
		room     int
		feedback string
		blocked  bool
	}{
		{"spare position", 1024, "", true},
		{"enough room", 1025, "", false},
		{"notice consumes room", 1025, strings.Repeat("context guidance ", 100), true},
		{"oversized prompt", -1, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess, err := eng.NewSession(initial + tc.room)
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close()
			called := false
			var usage ContextUsageEvent
			d := NewGenerationDriver(DriverOptions{
				Engine: eng, Session: sess, Tools: reg, ThinkMode: ds4.ThinkNone, MinResponseTokens: 1024,
				ContextFeedback: func(u ContextUsageEvent) string {
					if u.PromptTokens != initial {
						t.Fatalf("initial usage=%+v", u)
					}
					return tc.feedback
				},
				OnEvent: func(e Event) {
					if u, ok := e.(ContextUsageEvent); ok {
						usage = u
					}
				},
				CompletePrompt: func(p *ds4.Prompt, _ ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
					called = true
					if usage.PromptTokens != p.Tokens.Len() {
						t.Fatal("usage does not match actual prompt")
					}
					return "Done", nil
				},
			})
			res, err := d.RunWithPrompt(context.Background(), "sys", history)
			if errors.Is(err, ErrContextBudget) != tc.blocked || (err != nil && !tc.blocked) {
				t.Fatalf("err=%v", err)
			}
			if called == tc.blocked {
				t.Fatalf("generation called=%v", called)
			}
			if len(res.History) == 0 || res.History[0].Content != "draw" {
				t.Fatal("lost user history")
			}
			if tc.blocked && res.Assistant.Content != "" {
				t.Fatal("reported an older answer as output of the blocked run")
			}
			if tc.feedback != "" && usage.PromptTokens <= initial {
				t.Fatal("notice not counted")
			}
		})
	}
}

func TestContextPressureRetainsExecutedToolHistory(t *testing.T) {
	for _, failInGeneration := range []bool{false, true} {
		eng, _, reg := mockDriverEnv(t)
		sess, err := eng.NewSession(3072)
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		block, err := dsml.RenderToolCalls([]dsml.ToolCall{{Name: "svg_append", Arguments: `{}`}})
		if err != nil {
			t.Fatal(err)
		}
		rounds, executed := 0, 0
		d := NewGenerationDriver(DriverOptions{
			Engine: eng, Session: sess, Tools: reg, MinResponseTokens: 1024,
			CompletePrompt: func(_ *ds4.Prompt, _ ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
				rounds++
				if rounds == 2 {
					return "unfinished output", ds4.ErrContextFull
				}
				return block, nil
			},
			ExecuteTools: func(_ context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
				executed++
				content := "saved"
				if !failInGeneration {
					content = strings.Repeat("large tool result ", 750)
				}
				return []ds4.ChatMessage{{Role: "tool", ToolCallID: calls[0].ID, Content: content}}, nil
			},
		})
		res, err := d.RunWithPrompt(context.Background(), "sys", []ds4.ChatMessage{{Role: "user", Content: "draw"}})
		want := ErrContextBudget
		if failInGeneration {
			want = ds4.ErrContextFull
		}
		if !errors.Is(err, want) {
			t.Fatalf("err=%v want=%v", err, want)
		}
		if executed != 1 || res.ToolRounds != 1 || len(res.History) != 3 || res.History[2].Role != "tool" {
			t.Fatalf("lost completed work: %+v, executed=%d", res, executed)
		}
		if !failInGeneration && rounds != 1 {
			t.Fatal("generated with oversized tool result")
		}
	}
}

func TestContextUsageIncludesVisionTokens(t *testing.T) {
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(ds4.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(16384)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	enc := ds4.NewImageEncoder(eng)
	defer enc.SetLimits(0, 0)
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	history := []ds4.ChatMessage{{Role: "user", Parts: []ds4.ContentPart{{Text: "inspect"}, {Image: &ds4.ImageInput{Data: b.Bytes()}}}}}
	var usage ContextUsageEvent
	d := NewGenerationDriver(DriverOptions{
		Engine: eng, Session: sess, Tools: ds4.NewToolRegistry(), Images: enc, MinResponseTokens: 1024,
		ContextFeedback: func(u ContextUsageEvent) string { return "context notice" },
		OnEvent: func(e Event) {
			if u, ok := e.(ContextUsageEvent); ok {
				usage = u
			}
		},
		CompletePrompt: func(p *ds4.Prompt, _ ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
			if len(p.Images) != 1 || usage.PromptTokens != p.Tokens.Len() {
				t.Fatalf("incorrect image usage: %+v", usage)
			}
			span := p.Images[0]
			if span.Embedding.TokenCount() <= 0 || span.TokenStart+span.Embedding.TokenCount() > usage.PromptTokens {
				t.Fatal("image span not counted")
			}
			return "Done", nil
		},
	})
	if _, err := d.RunWithPrompt(context.Background(), "sys", history); err != nil {
		t.Fatal(err)
	}
}
