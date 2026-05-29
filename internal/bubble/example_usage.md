# Using internal/bubble (early sketch)

This package is intended to reduce duplication across svgpad, glyphpad, and cadpad for the "run ds4go generation from a Bubble Tea UI with good observability" problem.

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
