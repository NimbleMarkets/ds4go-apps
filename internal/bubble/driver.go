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
	Images    *ds4.ImageEncoder // required when history or tool results contain images
	ThinkMode ds4.ThinkMode
	MaxRounds int

	// FinalResponseOnly removes tool definitions on the final allowed turn and
	// adds a system instruction to summarize completed and unfinished work.
	// Calls emitted anyway remain subject to the hard execution limit.
	FinalResponseOnly bool

	// MaxTokens bounds each assistant turn's token budget (shared across
	// think-recovery resumes inside ds4go). Values <= 0 default to 1024.
	MaxTokens int

	// Temperature, TopP, and Seed configure sampling for every turn.
	// Temperature <= 0 keeps ds4go's argmax (greedy) decoding; when > 0,
	// ds4go's tool loop still forces greedy inside tool-call markup via
	// its own SampleControl, so only free content is sampled.
	Temperature float32
	TopP        float32
	Seed        uint64

	// CompleteTurn overrides the per-turn generation path. When nil the
	// driver uses ds4.ToolLoop.CompletePrompt — stream-driven generation with
	// early stop at tool-block close and live think recovery. Primarily a
	// test seam, mirroring ds4.ToolLoop.CompleteFunc.
	CompleteTurn func(prompt *ds4.Tokens, opts ds4.GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error)

	// CompletePrompt overrides generation with access to image spans. It takes
	// precedence over CompleteTurn; the driver retains ownership of the prompt.
	CompletePrompt func(prompt *ds4.Prompt, opts ds4.GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error)

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

	// PrepareHistory optionally trims observations or adds host guidance before
	// each model round. round is zero-based; maxRounds includes the final turn.
	// It must not mutate its input; its result becomes the retained history.
	PrepareHistory func(history []ds4.ChatMessage, round, maxRounds int) []ds4.ChatMessage

	// CompactHistory may replace history after measuring the actual prompt,
	// including images and schemas. Called once per round; must not mutate its
	// input. A changed history is rebuilt and checked before generation, without
	// resetting the tool-round budget or executing any calls again.
	CompactHistory func(context.Context, []ds4.ChatMessage, ContextUsageEvent) ([]ds4.ChatMessage, bool, error)

	// ResponseReserveTokens, when positive, caps generation to remaining space
	// minus this reserve. Zero preserves the existing MaxTokens behavior.
	ResponseReserveTokens int

	// ContextFeedback optionally adds guidance based on the rendered prompt.
	// Its message is appended and the prompt is rebuilt before the capacity
	// check and ContextUsageEvent, so the notice's own tokens are counted too.
	ContextFeedback func(ContextUsageEvent) string

	// MinResponseTokens opts into preflight protection: stop before generation
	// if fewer tokens remain, plus the runtime's required spare position.
	// Zero disables the check for hosts that manage their own context policy.
	MinResponseTokens int
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
	msg, err := d.opts.Tools.ParseAssistantSyntax(ds4.ToolSyntax(d.opts.Engine), text, d.opts.Engine.ThinkModeEnabled(d.opts.ThinkMode))
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
	Assistant       ds4.ChatMessage
	ToolRounds      int
	History         []ds4.ChatMessage
	BudgetExhausted bool // reached the final, non-tool-capable turn
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

	completePrompt := d.opts.CompletePrompt
	if completePrompt == nil && d.opts.CompleteTurn != nil {
		completePrompt = func(p *ds4.Prompt, opts ds4.GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error) {
			if len(p.Images) > 0 {
				return "", ds4.ErrCompleteFuncCannotCarryImages
			}
			return d.opts.CompleteTurn(p.Tokens, opts, onEvent)
		}
	}
	if completePrompt == nil {
		loop := ds4.ToolLoop{
			Engine:    d.opts.Engine,
			Session:   d.opts.Session,
			Tools:     d.opts.Tools,
			ThinkMode: d.opts.ThinkMode,
			Thinking:  d.opts.Engine.ThinkModeEnabled(d.opts.ThinkMode),
		}
		completePrompt = loop.CompletePrompt
	}

	working := append([]ds4.ChatMessage(nil), history...)
	toolRounds := 0
	budgetExhausted := false
	syntaxRetried := false
	var lastAssistant ds4.ChatMessage
	partial := func() RunResult {
		return RunResult{History: working, ToolRounds: toolRounds, Assistant: lastAssistant, BudgetExhausted: budgetExhausted}
	}

	for round := 0; ; round++ {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				d.opts.OnEvent(ErrorEvent{Err: err})
				return partial(), err
			}
		}

		budgetExhausted = round >= maxRounds-1
		promptTools, turnSystem := d.opts.Tools, system
		if budgetExhausted && d.opts.FinalResponseOnly {
			promptTools = ds4.NewToolRegistry()
			promptTools.SetReplayStore(d.opts.Tools.ReplayStore())
			turnSystem += "\nFinal response only: the tool budget is exhausted and all tools are unavailable, including scratchpad tools. Do not emit tool calls or tool markup. Summarize only confirmed completed work and explicitly state remaining work. Do not claim unexecuted operations succeeded. The application will save a checkpoint after this response."
		}
		d.opts.OnEvent(RoundStartedEvent{Round: round})
		if d.opts.PrepareHistory != nil {
			working = d.opts.PrepareHistory(working, round, maxRounds)
		}

		prompt, err := promptTools.BuildPromptMultimodal(d.opts.Engine, d.opts.Images, turnSystem, working, d.opts.ThinkMode)
		if err != nil {
			d.opts.OnEvent(ErrorEvent{Err: err})
			return partial(), err
		}
		usage := ContextUsageEvent{PromptTokens: prompt.Tokens.Len(), Capacity: d.opts.Session.Ctx()}
		if d.opts.CompactHistory != nil {
			next, changed, compactErr := d.opts.CompactHistory(ctx, working, usage)
			if compactErr != nil {
				prompt.Free()
				d.opts.OnEvent(ErrorEvent{Err: compactErr})
				return partial(), compactErr
			}
			if changed {
				candidate, err := promptTools.BuildPromptMultimodal(d.opts.Engine, d.opts.Images, turnSystem, next, d.opts.ThinkMode)
				if err != nil {
					prompt.Free()
					d.opts.OnEvent(ErrorEvent{Err: err})
					return partial(), err
				}
				// A short history can grow when a checkpoint is added. Keep
				// the original unless compaction actually creates more room.
				if candidate.Tokens.Len() < usage.PromptTokens {
					before := usage.PromptTokens
					prompt.Free()
					prompt, working = candidate, next
					usage.PromptTokens = prompt.Tokens.Len()
					d.opts.OnEvent(ContextCompactedEvent{Before: before, After: usage.PromptTokens})
				} else {
					candidate.Free()
				}
			}
		}
		if d.opts.ContextFeedback != nil {
			if note := d.opts.ContextFeedback(usage); note != "" {
				prompt.Free()
				working = append(working, ds4.ChatMessage{Role: "user", ToolCallID: ContextFeedbackID, Content: note})
				prompt, err = promptTools.BuildPromptMultimodal(d.opts.Engine, d.opts.Images, turnSystem, working, d.opts.ThinkMode)
				if err != nil {
					d.opts.OnEvent(ErrorEvent{Err: err})
					return partial(), err
				}
				usage.PromptTokens = prompt.Tokens.Len()
			}
		}
		d.opts.OnEvent(usage)
		if d.opts.MinResponseTokens > 0 && usage.Remaining() <= d.opts.MinResponseTokens {
			prompt.Free()
			d.opts.OnEvent(ErrorEvent{Err: ErrContextBudget})
			return partial(), ErrContextBudget
		}

		genOpts := ds4.GenerateOptions{
			MaxTokens:   maxTokens,
			Temperature: d.opts.Temperature,
			TopP:        d.opts.TopP,
			Seed:        d.opts.Seed,
			StopOnEOS:   true,
			Context:     ctx,
			OnToken: func(token int) {
				if part, e := d.opts.Engine.TokenText(token); e == nil {
					d.opts.OnEvent(TokenEvent{Text: part})
				}
			},
		}
		if d.opts.ResponseReserveTokens > 0 {
			genOpts.MaxTokens = min(genOpts.MaxTokens, usage.Remaining()-d.opts.ResponseReserveTokens)
			if genOpts.MaxTokens <= 0 {
				prompt.Free()
				d.opts.OnEvent(ErrorEvent{Err: ErrContextBudget})
				return partial(), ErrContextBudget
			}
		}
		text, err := completePrompt(prompt, genOpts, func(ev dsml.StreamEvent) {
			d.opts.OnEvent(StreamEvent{Event: ev})
		})
		prompt.Free()
		if err != nil {
			d.opts.OnEvent(ErrorEvent{Err: err})
			return partial(), err
		}

		assistant, err := d.ParseAssistant(text)
		if err != nil {
			return partial(), err
		}
		working = append(working, assistant)
		lastAssistant = assistant

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
					Content: dsml.ToolSyntaxErrorMessageSyntax(ds4.ToolSyntax(d.opts.Engine), assistant.MalformedReason),
				})
				d.opts.OnEvent(RoundCompletedEvent{Round: round})
				continue
			}
			d.opts.OnEvent(RoundCompletedEvent{Round: round})
			return partial(), nil
		}
		syntaxRetried = false

		if budgetExhausted {
			// Pair every rejected call with an observation so continuing the conversation
			// cannot mistake it for an executed operation or leave dangling call IDs.
			rejected := make([]ds4.ChatMessage, 0, len(assistant.ToolCalls))
			for _, call := range assistant.ToolCalls {
				rejected = append(rejected, ds4.ChatMessage{Role: "tool", ToolCallID: call.ID,
					Content: "NOT EXECUTED: " + call.Name + ". Tool round budget exhausted; no operation was performed. Report this as unfinished work."})
			}
			working = append(working, rejected...)
			d.opts.OnEvent(ToolResultsEvent{Results: rejected})
			d.opts.OnEvent(ErrorEvent{Err: ErrMaxRounds})
			return partial(), ErrMaxRounds
		}

		// Delegate execution to host (cadpad will run registry.Execute + side effects + Emit logs)
		results, err := d.ExecuteToolCalls(ctx, assistant.ToolCalls)
		if err != nil {
			return partial(), err
		}
		working = append(working, results...)
		toolRounds++

		d.opts.OnEvent(RoundCompletedEvent{Round: round})
	}
}
