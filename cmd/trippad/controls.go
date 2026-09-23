package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/runconfig"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/memory"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
)

func (m *model) key(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if k == "ctrl+f" || (k == "f" && (!m.input.Focused() || m.fullscreen)) {
		return m.toggleFullscreen()
	}
	if m.fullscreen {
		switch k {
		case "esc":
			return m.toggleFullscreen()
		case "q":
			return m.requestQuit()
		case "space", " ":
			m.playing = !m.playing
		}
		return nil
	}
	if k == "ctrl+o" {
		return m.openModelPicker()
	}
	if k == "tab" {
		if m.input.Focused() {
			m.input.Blur()
			return nil
		}
		return m.input.Focus()
	}
	if k == "esc" {
		if m.gen != nil {
			m.gen.Cancel()
			m.status = "Canceling…"
		}
		m.input.Blur()
		return nil
	}
	if m.input.Focused() {
		if k == "enter" {
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return nil
			}
			if !strings.HasPrefix(text, "/") && (m.gen != nil || m.action != nil || m.engine == nil) {
				m.status = "Wait for the model or current generation; Esc cancels"
				return nil
			}
			m.input.SetValue("")
			m.input.Blur()
			return m.submit(text)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return cmd
	}
	switch k {
	case "q":
		return m.requestQuit()
	case "e", "enter":
		return m.input.Focus()
	case "/":
		m.input.SetValue("/")
		return m.input.Focus()
	case "space", " ":
		m.playing = !m.playing
		m.status = "Playback toggled"
	case "?":
		m.inspect = inspector{kind: "help"}
	case "up":
		m.selected = max(0, m.selected-1)
	case "down":
		m.selected = min(max(0, len(m.state.Snapshot().Source.Params)-1), m.selected+1)
	case "left", "right", "shift+left", "shift+right":
		steps := 1
		if strings.HasPrefix(k, "shift+") {
			steps = 10
		}
		if strings.HasSuffix(k, "left") {
			steps = -steps
		}
		if err := m.state.Nudge(m.selected, steps); err != nil {
			m.status = err.Error()
		}
		m.dirty = true
	case "R", "shift+r":
		m.state.Randomize()
		m.dirty = true
	case "[", "]":
		delta := 1
		if k == "[" {
			delta = -1
		}
		m.starter = (m.starter + delta + len(shader.Starters())) % len(shader.Starters())
		return m.useStarter(m.starter)
	case "+", "=":
		m.setDownscale(max(1, m.downscale-1)) // finer raster
	case "-":
		m.setDownscale(min(8, m.downscale+1)) // coarser raster
	}
	return m.renderCmd()
}
func (m *model) runAction(label string, fn func() error) tea.Cmd {
	if m.action != nil {
		m.status = "Wait for the current preset operation"
		return nil
	}
	var cmd tea.Cmd
	m.action, cmd = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		err := fn()
		bubble.Send(ctx, ch, actionMsg{text: label, err: err})
	})
	m.status = label + "…"
	return cmd
}
func (m *model) useStarter(index int) tea.Cmd {
	src := shader.Starters()[index]
	state := m.state
	rev := state.Snapshot().Revision
	return m.runAction("Loaded "+src.Name, func() error { return state.Replace(src, nil, rev) })
}
func (m *model) submit(text string) tea.Cmd {
	if strings.HasPrefix(text, "/") {
		return m.slash(text)
	}
	if m.gen != nil || m.action != nil {
		m.status = "Wait for the current operation; Esc cancels generation"
		return nil
	}
	if m.engine == nil || m.session == nil {
		m.status = "No model ready; use /model or start with --model"
		return nil
	}
	reg, err := tools.Register(m.state, m.engine.HasVision(), m.language)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	if m.memory != nil {
		if err := m.memory.Register(reg); err != nil {
			m.status = err.Error()
			return nil
		}
	}
	engine, session, opts := m.engine, m.session, m.options
	mem, state := m.memory, m.state
	thinkMode := m.thinkMode
	m.activeThinkMode = thinkMode
	m.activity = nil
	if m.inspect.kind == "thinking" {
		m.inspect.top = -1
	}
	app := m.app
	history := append([]ds4.ChatMessage(nil), m.history...)
	history = append(history, ds4.ChatMessage{Role: "user", Content: text})
	m.addLog("> " + text)
	m.status = "Generating…"
	var cmd tea.Cmd
	m.gen, cmd = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		if mem != nil {
			if err := mem.Set(ctx, "latest-request", clipped(text, 12000)); err != nil {
				ch <- doneMsg{result: bubble.RunResult{History: history}, err: err}
				return
			}
		}
		if next, e := memoryHistory(ctx, history, mem); e != nil {
			ch <- doneMsg{result: bubble.RunResult{History: history}, err: e}
			return
		} else {
			history = next
		}
		var images *ds4.ImageEncoder
		if engine.HasVision() {
			images = ds4.NewImageEncoder(engine)
			defer images.SetLimits(0, 0)
		}
		var driver *bubble.GenerationDriver
		driver = bubble.NewGenerationDriver(bubble.DriverOptions{
			Engine: engine, Session: session, Tools: reg, Images: images, ThinkMode: thinkMode, MaxTokens: 4096, MaxRounds: opts.ToolRounds + 1, FinalResponseOnly: true,
			Temperature: opts.Temperature, TopP: opts.TopP, Seed: opts.EffectiveSeed(), PrepareHistory: prepareHistory, ContextFeedback: runconfig.ContextFeedback, MinResponseTokens: 1024, ResponseReserveTokens: 64,
			CompactHistory: func(ctx context.Context, h []ds4.ChatMessage, u bubble.ContextUsageEvent) ([]ds4.ChatMessage, bool, error) {
				if u.PromptTokens*4 < u.Capacity*3 && u.Remaining() >= 4608 {
					return h, false, nil
				}
				next, err := compactContext(ctx, h, state, mem)
				if err != nil {
					return h, false, err
				}
				// Restore the current round notice without changing the budget.
				for _, msg := range h {
					if msg.ToolCallID == "pad.tool-budget" {
						next = append(next, msg)
					}
				}
				return next, true, nil
			},
			OnEvent: func(ev bubble.Event) {
				if token, ok := ev.(bubble.TokenEvent); ok && app != nil && app.Flags != nil && app.Flags.Debug && app.Logger != nil {
					app.Logger.Printf("[token] %s", token.Text)
				}
				switch ev.(type) {
				case bubble.ContextUsageEvent, bubble.ContextCompactedEvent, bubble.StreamEvent, bubble.LogEvent, bubble.ToolResultsEvent, bubble.AssistantMessageEvent, bubble.RoundStartedEvent, bubble.MalformedRetryEvent:
					bubble.Send(ctx, ch, driverMsg{event: ev})
				}
			},
			ExecuteTools: func(ctx context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
				for _, call := range calls {
					driver.Emit(bubble.LogEvent{Message: "[tool] " + call.Name})
				}
				return reg.ExecuteToolCalls(ctx, calls)
			},
		})
		system := tools.SystemPrompt
		if mem != nil {
			system += "\n" + memory.SystemHint
		}
		res, err := driver.RunWithPrompt(ctx, system, history)
		// Finalize on this worker: checkpoint I/O must never block the TUI.
		ch <- finishRun(ctx, res, err, state, mem)
	})
	return cmd
}

// Budget notices describe the current round. Retaining older notices wastes
// context and can make a later request appear to have no tool budget left.
func prepareHistory(history []ds4.ChatMessage, round, maxRounds int) []ds4.ChatMessage {
	return runconfig.PrepareHistory(leanHistory(history), round, maxRounds)
}
func (m *model) slash(text string) tea.Cmd {
	fields := strings.Fields(text)
	command := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(text, command))
	switch command {
	case "/memory", "/compact":
		if m.action != nil || (command == "/compact" && m.gen != nil) {
			m.status = "Wait for the current operation; Esc cancels generation"
			return nil
		}
		mem, state := m.memory, m.state
		history := append([]ds4.ChatMessage(nil), m.history...)
		if command == "/memory" && mem == nil {
			m.status = "Memory is unavailable"
			return nil
		}
		details, count := m.contextDetails(), m.compactions
		var cmd tea.Cmd
		m.action, cmd = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) {
			defer close(ch)
			if command == "/compact" {
				next, err := compactContext(ctx, history, state, mem)
				ch <- compactedMsg{history: next, err: err}
			} else {
				brief, err := mem.Brief(ctx)
				ch <- memoryViewMsg{text: fmt.Sprintf("Memory: %s\nSession: %s\n%s\nCheckpoints: %d\nResume notes with --memory-session %s\n\n%s", mem.Dir, mem.Session, details, count, mem.Session, brief), err: err}
			}
		})
		return cmd
	case "/fullscreen":
		return m.toggleFullscreen()
	case "/downscale":
		if arg == "" {
			m.setDownscale(m.downscale)
			break
		}
		n, err := strconv.Atoi(arg)
		if err != nil {
			n = 0
		}
		m.setDownscale(n)
	case "/quit":
		return m.requestQuit()
	case "/help":
		m.inspect = inspector{kind: "help"}
	case "/pause":
		m.playing = false
	case "/play":
		m.playing = true
		m.dirty = true
	case "/randomize":
		m.state.Randomize()
		m.dirty = true
	case "/describe":
		snap := m.state.Snapshot()
		m.addLog(fmt.Sprintf("%s · %.1f fps · %v", snap.Source.Name, snap.FPS, snap.Named))
	case "/save", "/load":
		if arg == "" {
			m.status = "Usage: " + command + " FILE"
			return nil
		}
		state := m.state
		if command == "/save" {
			return m.runAction("Saved "+arg, func() error { return state.Save(arg) })
		}
		return m.runAction("Loaded "+arg, func() error { return state.Load(arg) })
	case "/set":
		if len(fields) != 3 {
			m.status = "Usage: /set NAME VALUE"
			return nil
		}
		value, err := strconv.ParseFloat(fields[2], 32)
		if err == nil {
			_, err = m.state.Set(fields[1], float32(value))
		}
		if err != nil {
			m.status = err.Error()
		} else {
			m.status = "Parameter updated"
			m.dirty = true
		}
	case "/preset":
		for i, src := range shader.Starters() {
			if src.Name == arg {
				m.starter = i
				return m.useStarter(i)
			}
		}
		m.status = "Presets: plasma, kaleidoscope, tunnel, fbm-warp"
	case "/model":
		return m.openModelPicker()
	case "/gallery":
		return m.openGallery()
	default:
		m.status = "Unknown command; /help lists commands"
	}
	return m.renderCmd()
}
