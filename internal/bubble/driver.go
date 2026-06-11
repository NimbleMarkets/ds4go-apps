package bubble

import (
	"context"
	"errors"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/dsml"
)

// ErrMaxRounds reports that a run hit MaxRounds with tool calls still
// pending. The RunResult returned alongside it carries the partial history
// and last assistant turn so hosts can still act on what was produced.
var ErrMaxRounds = errors.New("bubble: tool loop exceeded maximum rounds")

// DriverOptions configures a GenerationDriver.
type DriverOptions struct {
	Engine    *ds4.Engine
	Session   *ds4.Session
	Tools     *ds4.ToolRegistry
	ThinkMode ds4.ThinkMode
	MaxRounds int

	// MaxTokens bounds each assistant turn's token budget (shared across
	// think-recovery resumes inside ds4go). Values <= 0 default to 1024.
	MaxTokens int

	// CompleteTurn overrides the per-turn generation path. When nil the
	// driver uses ds4.ToolLoop.CompleteTurn — stream-driven generation with
	// early stop at tool-block close and live think recovery. Primarily a
	// test seam, mirroring ds4.ToolLoop.CompleteFunc.
	CompleteTurn func(prompt *ds4.Tokens, opts ds4.GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error)

	// OnEvent is called for every interesting thing that happens during
	// generation. This is the primary hook for rich logging into UI boxes,
	// metrics collection, and custom side effects.
	OnEvent func(Event)

	// ExecuteTools is called when the model emits tool calls.
	// The driver does NOT execute tools itself — the host app is responsible.
	//
	// This is critical for cadpad: the app can run lua_* tools, update
	// its World, populate luaRuntimeLog, etc., then return the tool results.
	ExecuteTools func(ctx context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error)
}

// GenerationDriver provides observable, controllable tool-calling generation
// on top of ds4go. It is designed to replace direct use of ToolLoop in
// situations where the app needs rich visibility (generation logs, tool metrics,
// custom Lua execution, etc.).
type GenerationDriver struct {
	opts DriverOptions
}

// NewGenerationDriver creates a new driver with the given options.
func NewGenerationDriver(opts DriverOptions) *GenerationDriver {
	if opts.OnEvent == nil {
		opts.OnEvent = func(Event) {} // no-op default
	}
	if opts.MaxRounds <= 0 {
		opts.MaxRounds = 8
	}
	return &GenerationDriver{opts: opts}
}

// BuildPrompt is a thin wrapper.
func (d *GenerationDriver) BuildPrompt(system string, history []ds4.ChatMessage) (*ds4.Tokens, error) {
	return d.opts.Tools.BuildPrompt(d.opts.Engine, system, history, d.opts.ThinkMode)
}

// ParseAssistant wraps parsing and emits an AssistantMessageEvent.
func (d *GenerationDriver) ParseAssistant(text string) (ds4.ChatMessage, error) {
	msg, err := d.opts.Tools.ParseAssistant(text, d.opts.ThinkMode != ds4.ThinkNone)
	if err != nil {
		d.opts.OnEvent(ErrorEvent{Err: err})
		return ds4.ChatMessage{}, err
	}
	d.opts.OnEvent(AssistantMessageEvent{Message: msg})
	return msg, nil
}

// ExecuteToolCalls runs tools via the provided callback and emits visibility events.
func (d *GenerationDriver) ExecuteToolCalls(ctx context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
	if d.opts.ExecuteTools == nil {
		return nil, nil // or error
	}

	d.opts.OnEvent(ToolCallsEvent{Calls: calls})

	results, err := d.opts.ExecuteTools(ctx, calls)
	if err != nil {
		d.opts.OnEvent(ErrorEvent{Err: err})
		return nil, err
	}

	d.opts.OnEvent(ToolResultsEvent{Results: results})
	return results, nil
}

// Emit lets the app inject custom LogEvents (ideal for cadpad Lua logging).
func (d *GenerationDriver) Emit(e Event) {
	d.opts.OnEvent(e)
}

// RunResult is the final snapshot after a RunWithPrompt (similar to ds4.ToolLoopResult).
type RunResult struct {
	Assistant  ds4.ChatMessage
	ToolRounds int
	History    []ds4.ChatMessage
}

// RunWithPrompt executes a full multi-round observable tool-calling
// generation on the library's stream-driven turn path: each round stops
// early once the tool block closes, recovers tool calls opened inside an
// unclosed think block, and forwards live dsml.StreamEvents (as
// StreamEvent) alongside the raw-text TokenEvent path. A turn whose DSML
// could not be parsed gets one consecutive syntax-error retry, mirroring
// ds4.ToolLoop.Run. Hitting MaxRounds with tool calls still pending returns
// the partial RunResult together with ErrMaxRounds.
//
// Events emitted during the run (via OnEvent):
//   - RoundStartedEvent / RoundCompletedEvent around each assistant turn
//   - TokenEvent for every token chunk (raw text, for streaming UIs)
//   - StreamEvent for every decoder delta (reasoning/content/tool calls)
//   - AssistantMessageEvent after ParseAssistant (includes reasoning if enabled)
//   - MalformedRetryEvent before a syntax-error retry turn
//   - ToolCallsEvent before delegating to ExecuteTools
//   - ToolResultsEvent after the host callback returns
//   - LogEvent (only via explicit d.Emit calls from the host, e.g. from inside
//     its ExecuteTools closure or lua tool wrappers)
//   - ErrorEvent on failures
//
// The provided ExecuteTools callback is responsible for actually invoking the
// tool registry (or custom handlers) and performing side effects (e.g. lua_run
// mutating the World, writing files, refreshing previews). The driver only
// orchestrates and observes.
func (d *GenerationDriver) RunWithPrompt(ctx context.Context, system string, history []ds4.ChatMessage) (RunResult, error) {
	if d.opts.Engine == nil {
		return RunResult{}, errors.New("bubble: nil engine in driver")
	}
	if d.opts.Session == nil {
		return RunResult{}, errors.New("bubble: nil session in driver")
	}
	if d.opts.Tools == nil {
		return RunResult{}, errors.New("bubble: nil tools in driver")
	}
	if d.opts.ExecuteTools == nil {
		// Non-fatal for pure-text models, but most cadpad runs need it.
		d.opts.OnEvent(LogEvent{Level: "warn", Message: "no ExecuteTools callback; tool calls will be ignored"})
	}

	maxRounds := d.opts.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 8
	}
	maxTokens := d.opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	completeTurn := d.opts.CompleteTurn
	if completeTurn == nil {
		loop := ds4.ToolLoop{
			Engine:    d.opts.Engine,
			Session:   d.opts.Session,
			Tools:     d.opts.Tools,
			ThinkMode: d.opts.ThinkMode,
			Thinking:  d.opts.ThinkMode != ds4.ThinkNone,
		}
		completeTurn = loop.CompleteTurn
	}

	working := append([]ds4.ChatMessage(nil), history...)
	toolRounds := 0
	syntaxRetried := false

	for round := 0; ; round++ {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				d.opts.OnEvent(ErrorEvent{Err: err})
				return RunResult{}, err
			}
		}

		d.opts.OnEvent(RoundStartedEvent{Round: round})

		prompt, err := d.BuildPrompt(system, working)
		if err != nil {
			d.opts.OnEvent(ErrorEvent{Err: err})
			return RunResult{}, err
		}

		genOpts := ds4.GenerateOptions{
			MaxTokens: maxTokens,
			StopOnEOS: true,
			Context:   ctx,
			OnToken: func(token int) {
				if part, e := d.opts.Engine.TokenText(token); e == nil {
					d.opts.OnEvent(TokenEvent{Text: part})
				}
			},
		}
		text, err := completeTurn(prompt, genOpts, func(ev dsml.StreamEvent) {
			d.opts.OnEvent(StreamEvent{Event: ev})
		})
		prompt.Free()
		if err != nil {
			d.opts.OnEvent(ErrorEvent{Err: err})
			return RunResult{}, err
		}

		assistant, err := d.ParseAssistant(text)
		if err != nil {
			return RunResult{}, err
		}
		working = append(working, assistant)

		if len(assistant.ToolCalls) == 0 {
			// Same contract as ds4.ToolLoop.Run: a turn that degraded to
			// content gets one consecutive syntax-error retry while the
			// round budget allows; a second consecutive failure returns the
			// raw text as the answer.
			if assistant.MalformedReason != "" && !syntaxRetried && round < maxRounds-1 {
				syntaxRetried = true
				d.opts.OnEvent(MalformedRetryEvent{Reason: assistant.MalformedReason})
				working = append(working, ds4.ChatMessage{
					Role:    "tool",
					Content: dsml.ToolSyntaxErrorMessage(assistant.MalformedReason),
				})
				d.opts.OnEvent(RoundCompletedEvent{Round: round})
				continue
			}
			d.opts.OnEvent(RoundCompletedEvent{Round: round})
			return RunResult{
				Assistant:  assistant,
				ToolRounds: toolRounds,
				History:    working,
			}, nil
		}
		syntaxRetried = false

		if round >= maxRounds-1 {
			d.opts.OnEvent(ErrorEvent{Err: ErrMaxRounds})
			return RunResult{Assistant: assistant, ToolRounds: toolRounds, History: working}, ErrMaxRounds
		}

		// Delegate execution to host (cadpad will run registry.Execute + side effects + Emit logs)
		results, err := d.ExecuteToolCalls(ctx, assistant.ToolCalls)
		if err != nil {
			return RunResult{}, err
		}
		working = append(working, results...)
		toolRounds++

		d.opts.OnEvent(RoundCompletedEvent{Round: round})
	}
}


