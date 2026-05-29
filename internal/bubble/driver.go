package bubble

import (
	"context"
	"errors"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
)

// DriverOptions configures a GenerationDriver.
type DriverOptions struct {
	Engine    *ds4.Engine
	Session   *ds4.Session
	Tools     *ds4.ToolRegistry
	ThinkMode ds4.ThinkMode
	MaxRounds int

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

// RunWithPrompt executes a full multi-round observable tool-calling generation.
// It replaces direct ds4.ToolLoop usage when rich per-step visibility is needed.
//
// Events emitted during the run (via OnEvent):
//   - RoundStartedEvent / RoundCompletedEvent around each assistant turn
//   - TokenEvent for every token chunk (for streaming UIs)
//   - AssistantMessageEvent after ParseAssistant (includes reasoning if enabled)
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

	working := append([]ds4.ChatMessage(nil), history...)
	toolRounds := 0

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

		// Collect full assistant text while emitting per-token events.
		var text strings.Builder
		genOpts := ds4.GenerateOptions{
			MaxTokens: 1024,
			Context:   ctx,
			OnToken: func(token int) {
				if part, e := d.opts.Engine.TokenText(token); e == nil {
					text.WriteString(part)
					d.opts.OnEvent(TokenEvent{Text: part})
				}
			},
		}

		gen := ds4.Generator{Engine: d.opts.Engine, Session: d.opts.Session}
		if _, err := gen.GenerateTokens(prompt, genOpts); err != nil {
			prompt.Free()
			d.opts.OnEvent(ErrorEvent{Err: err})
			return RunResult{}, err
		}
		prompt.Free()

		assistant, err := d.ParseAssistant(text.String())
		if err != nil {
			return RunResult{}, err
		}
		working = append(working, assistant)

		if len(assistant.ToolCalls) == 0 {
			d.opts.OnEvent(RoundCompletedEvent{Round: round})
			return RunResult{
				Assistant:  assistant,
				ToolRounds: toolRounds,
				History:    working,
			}, nil
		}

		if round >= maxRounds-1 {
			err := errors.New("bubble: tool loop exceeded maximum rounds")
			d.opts.OnEvent(ErrorEvent{Err: err})
			return RunResult{}, err
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


