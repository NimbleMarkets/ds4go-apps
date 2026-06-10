# Using internal/bubble

This package is intended to reduce duplication across svgpad, glyphpad, and cadpad for the "run ds4go generation from a Bubble Tea UI with good observability" problem.

## Generation handle (Start, Wait, Cancel)

The Generation handle provides the low-level plumbing to wire a cancellable context into a background goroutine:

```go
// In Update, on submit:
var waitCmd tea.Cmd
m.gen, waitCmd = bubble.Start(m.generate)
cmds = append(cmds, waitCmd)

// The generate function:
func (m model) generate(ctx context.Context, ch chan<- tea.Msg) {
	defer close(ch)
	opts := ds4.GenerateOptions{Context: ctx, OnToken: func(tok int) {
		if text, err := m.engine.TokenText(tok); err == nil {
			select {
			case ch <- bubble.TokenMsg(text):
			default: // GenerateTokens stops via opts.Context; drop tokens when UI lags
			}
		}
	}}
	_, err := (ds4.Generator{Engine: m.engine, Session: m.session}).GenerateTokens(prompt, opts)
	ch <- bubble.DoneMsg{Err: err, CtxPos: m.session.Pos()}
}

Note: the DoneMsg send is intentionally blocking. The model's Update loop must keep re-arming `m.gen.Wait()` after every received message until DoneMsg arrives — that constant draining is what guarantees the send completes. Do not drop DoneMsg with a select/default: the app would stay in its generating state forever.

// On token: cmds = append(cmds, m.gen.Wait())
// On esc:   m.gen.Cancel()
// On done:  if m.gen.Canceled() { status = "Aborted" }; m.gen = nil
```

## Current recommended pattern for cadpad-style apps (tool heavy + custom side effects)

```go
driver := bubble.NewGenerationDriver(bubble.DriverOptions{
    Engine:    eng,
    Session:   sess,
    Tools:     reg,
    ThinkMode: ds4.ThinkHigh,
    OnEvent: func(e bubble.Event) {
        switch ev := e.(type) {
        case bubble.AssistantMessageEvent:
            if ev.Message.ReasoningContent != "" {
                m.generationLog = append(m.generationLog, "[REASONING] "+ev.Message.ReasoningContent)
            }
            // also record tool calls if present, etc.

        case bubble.ToolCallsEvent:
            for _, call := range ev.Calls {
                m.toolCallCounts[call.Name]++
                m.generationLog = append(m.generationLog, fmt.Sprintf("[TOOL CALL] %s", call.Name))
            }

        case bubble.ToolResultsEvent:
            for _, r := range ev.Results {
                m.generationLog = append(m.generationLog, "[TOOL RESULT] "+r.Content)
            }

        case bubble.LogEvent:
            // cadpad-specific: Lua writes, lua_run results, etc.
            if strings.Contains(ev.Message, "lua") {
                m.luaRuntimeLog = append(m.luaRuntimeLog, ev.Message)
            }
            m.generationLog = append(m.generationLog, ev.Message)
        }
    },
})

// In submitInputCmd (cadpad pattern):
ch := make(chan tea.Msg, 128)
go func() {
    defer close(ch)
    res, err := driver.RunWithPrompt(ctx, system, []ds4.ChatMessage{{Role: "user", Content: userText}})
    if err != nil {
        ch <- toolDoneMsg{err: err}
        return
    }
    ch <- toolDoneMsg{text: res.Assistant.Content, reasoning: res.Assistant.ReasoningContent}
}()
return m, tea.Batch(bubble.Wait(ch), inferencingStartCmd...)

The OnEvent + ExecuteTools closures are where the app:
- Calls driver.Emit(bubble.LogEvent{...}) for Lua runtime details, file writes, etc.
- Performs real tool execution (m.tools.ExecuteToolCalls) + side effects on World.
- Translates events into live UI updates for generationLog / luaRuntimeLog / toolRounds / toolCallCounts / toolCalls.

This gives cadpad full control to feed exactly what it wants into its Generation Log, Lua Runtime box, tool metrics, etc., while still using the shared driver + event types.

---

Future work can evolve this into a higher-level loop runner that still gives good hooks.
