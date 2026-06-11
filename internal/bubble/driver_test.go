package bubble

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
)

// mockDriverEnv builds an engine-free driver environment on the ds4 mock
// library: BuildPrompt and TokenText work, generation is overridden via the
// CompleteTurn seam.
func mockDriverEnv(t *testing.T) (*ds4.Engine, *ds4.Session, *ds4.ToolRegistry) {
	t.Helper()
	lib := ds4api.NewMockLibrary()
	eng, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	sess, err := eng.NewSession(256)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	reg := ds4.NewToolRegistry()
	if err := reg.RegisterFunc(ds4.ToolSchema{
		Name:        "svg_append",
		Description: "append a chunk",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		return "ok", nil
	}); err != nil {
		t.Fatalf("RegisterFunc: %v", err)
	}
	return eng, sess, reg
}

// decodeAsEvents replays text through a real StreamDecoder, forwarding every
// event — what the library path would emit for this completion.
func decodeAsEvents(text string, onEvent func(dsml.StreamEvent)) {
	dec := dsml.NewStreamDecoder(false)
	for _, ev := range dec.Write(text) {
		onEvent(ev)
	}
	more, _, _ := dec.Close()
	for _, ev := range more {
		onEvent(ev)
	}
}

func TestDriverStreamsEventsAndExecutesTools(t *testing.T) {
	eng, sess, reg := mockDriverEnv(t)

	toolBlock, err := dsml.RenderToolCalls([]dsml.ToolCall{{
		Name:      "svg_append",
		Arguments: `{"chunk":"<svg>"}`,
	}})
	if err != nil {
		t.Fatalf("RenderToolCalls: %v", err)
	}

	var events []Event
	turn := 0
	d := NewGenerationDriver(DriverOptions{
		Engine: eng, Session: sess, Tools: reg, MaxRounds: 3,
		OnEvent: func(e Event) { events = append(events, e) },
		ExecuteTools: func(ctx context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
			return reg.ExecuteToolCalls(ctx, calls)
		},
		CompleteTurn: func(prompt *ds4.Tokens, opts ds4.GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error) {
			turn++
			if turn == 1 {
				text := "drafting" + toolBlock
				decodeAsEvents(text, onEvent)
				return text, nil
			}
			return "done", nil
		},
	})

	res, err := d.RunWithPrompt(context.Background(), "sys", []ds4.ChatMessage{{Role: "user", Content: "draw"}})
	if err != nil {
		t.Fatalf("RunWithPrompt: %v", err)
	}
	if res.Assistant.Content != "done" || res.ToolRounds != 1 {
		t.Fatalf("result = %+v, want final content %q after 1 tool round", res, "done")
	}
	// user, assistant(tool calls), tool result, final assistant
	if len(res.History) != 4 || res.History[2].Role != "tool" || res.History[2].Content != "ok" {
		t.Fatalf("history = %+v", res.History)
	}

	var sawStart, sawEnd bool
	for _, e := range events {
		se, ok := e.(StreamEvent)
		if !ok {
			continue
		}
		switch se.Event.Type {
		case dsml.EventToolCallStart:
			sawStart = se.Event.Name == "svg_append"
		case dsml.EventToolCallEnd:
			// Arguments is JSON-marshaled (angle brackets escape to <),
			// so compare the decoded chunk value.
			var args struct {
				Chunk string `json:"chunk"`
			}
			if err := json.Unmarshal([]byte(se.Event.Arguments), &args); err == nil {
				sawEnd = args.Chunk == "<svg>"
			}
		}
	}
	if !sawStart || !sawEnd {
		t.Fatalf("missing forwarded stream events: start=%v end=%v", sawStart, sawEnd)
	}
}

// malformedCompletion is balanced (not repairable) but the invoke header
// has no name attribute, so ParseAssistant degrades it to plain content
// with a MalformedReason (same fixture as ds4-go's toolloop tests).
const malformedCompletion = "\n\n<｜DSML｜tool_calls>\n<｜DSML｜invoke>\n</｜DSML｜invoke>\n</｜DSML｜tool_calls>"

func TestDriverRetriesMalformedDSML(t *testing.T) {
	eng, sess, reg := mockDriverEnv(t)

	var events []Event
	turn := 0
	d := NewGenerationDriver(DriverOptions{
		Engine: eng, Session: sess, Tools: reg, MaxRounds: 4,
		OnEvent: func(e Event) { events = append(events, e) },
		CompleteTurn: func(prompt *ds4.Tokens, opts ds4.GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error) {
			turn++
			if turn == 1 {
				return malformedCompletion, nil
			}
			return "recovered answer", nil
		},
	})

	res, err := d.RunWithPrompt(context.Background(), "sys", []ds4.ChatMessage{{Role: "user", Content: "draw"}})
	if err != nil {
		t.Fatalf("RunWithPrompt: %v", err)
	}
	if turn != 2 {
		t.Fatalf("turns = %d, want a retry after the malformed turn", turn)
	}
	if res.Assistant.Content != "recovered answer" {
		t.Fatalf("Assistant.Content = %q", res.Assistant.Content)
	}
	// user, malformed assistant, tool syntax error, final assistant
	if len(res.History) != 4 {
		t.Fatalf("history length = %d, want 4: %+v", len(res.History), res.History)
	}
	if res.History[2].Role != "tool" || !strings.Contains(res.History[2].Content, "Tool error: invalid DSML tool call") {
		t.Fatalf("missing ToolSyntaxErrorMessage turn: %#v", res.History[2])
	}
	sawRetryEvent := false
	for _, e := range events {
		if _, ok := e.(MalformedRetryEvent); ok {
			sawRetryEvent = true
		}
	}
	if !sawRetryEvent {
		t.Fatal("expected a MalformedRetryEvent")
	}
}

func TestDriverSingleConsecutiveSyntaxRetry(t *testing.T) {
	eng, sess, reg := mockDriverEnv(t)

	turn := 0
	d := NewGenerationDriver(DriverOptions{
		Engine: eng, Session: sess, Tools: reg, MaxRounds: 6,
		CompleteTurn: func(prompt *ds4.Tokens, opts ds4.GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error) {
			turn++
			return malformedCompletion, nil
		},
	})

	res, err := d.RunWithPrompt(context.Background(), "sys", []ds4.ChatMessage{{Role: "user", Content: "draw"}})
	if err != nil {
		t.Fatalf("RunWithPrompt: %v", err)
	}
	if turn != 2 {
		t.Fatalf("turns = %d, want exactly one retry before giving up", turn)
	}
	if res.Assistant.Content != strings.TrimSpace(malformedCompletion) {
		t.Fatalf("Assistant.Content = %q, want the raw completion as content", res.Assistant.Content)
	}
}

func TestDriverMaxRoundsReturnsPartialResult(t *testing.T) {
	eng, sess, reg := mockDriverEnv(t)

	toolBlock, err := dsml.RenderToolCalls([]dsml.ToolCall{{
		Name:      "svg_append",
		Arguments: `{"chunk":"x"}`,
	}})
	if err != nil {
		t.Fatalf("RenderToolCalls: %v", err)
	}

	d := NewGenerationDriver(DriverOptions{
		Engine: eng, Session: sess, Tools: reg, MaxRounds: 2,
		ExecuteTools: func(ctx context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
			return reg.ExecuteToolCalls(ctx, calls)
		},
		CompleteTurn: func(prompt *ds4.Tokens, opts ds4.GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error) {
			return "more" + toolBlock, nil
		},
	})

	res, err := d.RunWithPrompt(context.Background(), "sys", []ds4.ChatMessage{{Role: "user", Content: "draw"}})
	if !errors.Is(err, ErrMaxRounds) {
		t.Fatalf("err = %v, want ErrMaxRounds", err)
	}
	if res.ToolRounds != 1 {
		t.Fatalf("ToolRounds = %d, want 1 executed round before the cap", res.ToolRounds)
	}
	if len(res.History) == 0 || len(res.Assistant.ToolCalls) == 0 {
		t.Fatalf("want partial history and pending assistant calls, got %+v", res)
	}
}

func TestDriverForwardsTokenEvents(t *testing.T) {
	eng, sess, reg := mockDriverEnv(t)
	_ = eng

	var tokens []string
	d := NewGenerationDriver(DriverOptions{
		Engine: eng, Session: sess, Tools: reg, MaxRounds: 2,
		OnEvent: func(e Event) {
			if te, ok := e.(TokenEvent); ok {
				tokens = append(tokens, te.Text)
			}
		},
		CompleteTurn: func(prompt *ds4.Tokens, opts ds4.GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error) {
			opts.OnToken(42) // mock engine TokenText(42) == "tok42"
			return "done", nil
		},
	})
	if _, err := d.RunWithPrompt(context.Background(), "sys", []ds4.ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("RunWithPrompt: %v", err)
	}
	if len(tokens) != 1 || tokens[0] != "tok42" {
		t.Fatalf("tokens = %v, want [tok42] — the raw-text TokenEvent path must keep working", tokens)
	}
}
