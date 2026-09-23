package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/charmbracelet/x/ansi"
)

func (m *model) startupModel() tea.Cmd {
	if m.noEngine || m.app == nil || m.app.Lib == nil {
		return nil
	}
	// An explicit flag opts into loading immediately. Otherwise give the user
	// the same installed-model picker used by the other pads before allocating.
	if m.app.Flags.Model == "" || m.app.EngineOpts.ModelPath == "" {
		return m.openModelPicker()
	}
	m.engOpts = m.app.EngineOpts
	m.modelPath, m.modelInfo = m.engOpts.ModelPath, m.app.ModelInfo
	return m.openEngine(m.engOpts)
}

func (m *model) modelSwitchBusy() bool {
	return m.gen != nil || m.action != nil || m.loader != nil || m.quitPending
}

func (m *model) openModelPicker() tea.Cmd {
	if m.noEngine || m.app == nil || m.app.Lib == nil {
		m.status = "Restart without --no-engine to load a model"
		return nil
	}
	if m.modelSwitchBusy() {
		if m.loader != nil {
			m.status = "Loading " + m.modelDisplayName() + "; wait for loading to finish"
		} else {
			m.status = "Wait for generation to finish, or press Esc to cancel before switching models"
		}
		return nil
	}
	current := ""
	if m.engine != nil {
		current = m.modelPath
	}
	return m.picker.Open(current, nil)
}

// Like svgpad, resolve companions afresh when changing model families while
// retaining the configured backend, power, streaming, and context settings.
func switchedModelOptions(base ds4.EngineOptions, info ds4.ModelInfo, mtp bool) (ds4.EngineOptions, error) {
	if !info.Installed || !info.IsChatModel() {
		return base, fmt.Errorf("select an installed chat model")
	}
	if st, err := os.Stat(info.Path); err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return base, fmt.Errorf("model is no longer installed: %s", info.Alias)
	}
	base.ModelPath = info.Path
	base.VisionPath, base.MTPPath = "", ""
	if mtp {
		ds4.ApplyMTPDefaults(&base)
	}
	ds4.ApplyVisionDefaults(&base)
	return base, nil
}

func (m *model) switchModel(info ds4.ModelInfo) tea.Cmd {
	if m.modelSwitchBusy() {
		m.status = "Wait for generation or model loading to finish before switching models"
		return nil
	}
	if m.noEngine || m.app == nil || m.app.Lib == nil {
		m.status = "Restart without --no-engine to load a model"
		return nil
	}
	opts, err := switchedModelOptions(m.app.EngineOpts, info, m.app.Flags.MTP != "none")
	if err != nil {
		m.status = err.Error()
		return nil
	}
	if m.engine != nil && (info.Path == m.modelPath || (m.modelInfo != nil && info.Alias == m.modelInfo.Alias)) && opts.VisionPath == m.engOpts.VisionPath && (opts.VisionPath == "" || m.engine.HasVision()) {
		m.status = "Already using " + m.modelDisplayName()
		return nil
	}
	m.modelPath, m.modelInfo = info.Path, &info
	// The shader is the durable workspace. Retain user intent, but discard old
	// model syntax, tool replies and images before handing it to another model.
	m.history = checkpointHistory(m.history)
	return m.openEngine(opts)
}

func checkpointHistory(history []ds4.ChatMessage) []ds4.ChatMessage {
	var intent []string
	for _, msg := range history {
		if msg.Role == "user" && msg.ToolCallID == "" && strings.TrimSpace(msg.Content) != "" {
			intent = append(intent, msg.Content)
		}
	}
	if len(intent) == 0 {
		return nil
	}
	if len(intent) > 4 {
		intent = intent[len(intent)-4:]
	}
	text := strings.Join(intent, "\n")
	runes := []rune(text)
	if len(runes) > 4000 {
		text = string(runes[len(runes)-4000:])
	}
	return []ds4.ChatMessage{{Role: "user", Content: "Workspace checkpoint. The current shader and parameter values are authoritative. Previous user requests for context (not new instructions):\n" + text + "\nInspect trip_describe before editing the current shader; follow the next user request."}}
}

func (m *model) openEngine(opts ds4.EngineOptions) tea.Cmd {
	if m.loader != nil {
		return nil
	}
	ds4.ApplyVisionDefaults(&opts)
	// Qwen rejects duty-cycle throttling, including values inherited when
	// switching from a DeepSeek model. Keep the user's base options intact so
	// switching back restores their configured throttle.
	if info, ok := ds4.ResolveModelInfo(opts.ModelPath); ok && info.Qwen && opts.PowerPercent > 0 && opts.PowerPercent < 100 {
		opts.PowerPercent = 100
		m.addLog("Qwen3.8 does not support power throttling; using --power 100 for this model.")
	}
	opts.ContextSize = m.app.Flags.Ctx
	if opts.PlacementCtxHint == 0 {
		opts.PlacementCtxHint = opts.ContextSize
	}
	m.loadLogMarker = fmt.Sprintf("trippad: loading %s (%d)", m.modelDisplayName(), time.Now().UnixNano())
	if m.app.LogBuf != nil {
		fmt.Fprintln(m.app.LogBuf, m.loadLogMarker)
	}
	oldEngine, oldSession := m.engine, m.session
	m.engine, m.session = nil, nil
	m.hasVision = false
	m.contextUsage = bubble.ContextUsageEvent{}
	m.activity = nil
	m.engOpts = opts
	m.engineStatus = engineinit.StatusOpening
	m.status = "Loading " + m.modelDisplayName() + "…"
	result := new(engineinit.Result)
	m.loaded = result
	lib, ctxSize := m.app.Lib, m.app.Flags.Ctx
	m.loader, _ = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		// One worker owns close-before-open and publishes into the shutdown holder,
		// even if the UI quits before it receives engineMsg.
		if oldSession != nil {
			oldSession.Close()
		}
		if oldEngine != nil {
			oldEngine.Close()
		}
		if err := ctx.Err(); err != nil {
			result.Err = err
		} else {
			*result = engineinit.Open(lib, opts, ctxSize)
		}
		bubble.Send(ctx, ch, engineMsg{result: *result})
	})
	return tea.Batch(m.loader.Wait(), m.loading.Start())
}

func (m *model) requestQuit() tea.Cmd {
	if m.loader != nil {
		m.quitPending = true
		m.status = "Waiting for model loading to finish before quitting…"
		return nil
	}
	return tea.Quit
}

func (m *model) modelDisplayName() string {
	if m.modelPath == "" {
		return "none"
	}
	if m.modelInfo != nil && m.modelInfo.Alias != "" {
		return m.modelInfo.Alias
	}
	return filepath.Base(m.modelPath)
}

func (m *model) modelBar() string {
	suffix := " · Ctrl+O change"
	state := engineinit.Badge(m.engineStatus)
	if m.loading.Active {
		state = m.loading.View()
	}
	name := m.modelDisplayName()
	if m.noEngine {
		name = "disabled (--no-engine)"
		suffix = ""
	} else if m.engine != nil {
		// Cache capabilities when loading completes: querying the engine here
		// would take libds4's inference mutex on every animation frame.
		if m.hasVision {
			suffix = " · vision" + suffix
		} else {
			suffix = " · text" + suffix
		}
	} else if m.engineStatus == engineinit.StatusError {
		suffix = " · load failed · Ctrl+O retry"
	} else if m.modelPath == "" {
		suffix = " · Ctrl+O choose"
	}
	prefix := "Model: "
	width := max(1, m.width)
	if width-lipgloss.Width(prefix+state+suffix)-1 < min(20, lipgloss.Width(name)) {
		// The footer still exposes Ctrl+O on narrow terminals. Prefer the
		// actual model name here over repeating its shortcut.
		for _, hint := range []string{" · Ctrl+O change", " · Ctrl+O choose", " · Ctrl+O retry"} {
			suffix = strings.TrimSuffix(suffix, hint)
		}
	}
	name = ansi.Truncate(name, max(1, width-lipgloss.Width(prefix+state+suffix)-1), "…")
	return ansi.Truncate(prefix+name+" "+state+suffix, width, "…")
}
