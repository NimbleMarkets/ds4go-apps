package main

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.MouseMotionMsg, tea.MouseWheelMsg:
		// Do not invalidate view cache for high frequency camera adjustments
	default:
		m.clearViewCache()
	}

	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m = m.resize()
		pc, pr := m.viewportInnerSize()
		m.logger.Printf("[SIZE] width=%d height=%d viewport=%dx%d", m.width, m.height, pc, pr)
		cmds = append(cmds, m.pic.SetSize(pc, pr))
		// Note: we do NOT auto-refresh the preview on every resize because
		// animated terminal resizes would trigger a CPU-render storm.
		// The picture widget scales the existing image. Press 'p' to re-render.

	case tea.KeyMsg:
		var cmd tea.Cmd
		m, cmd = m.handleKeyMsg(msg)
		cmds = append(cmds, cmd)

	case tea.MouseClickMsg:
		var cmd tea.Cmd
		m, cmd = m.handleMouseClick(msg)
		cmds = append(cmds, cmd)

	case tea.MouseReleaseMsg:
		var cmd tea.Cmd
		m, cmd = m.handleMouseRelease(msg)
		cmds = append(cmds, cmd)

	case tea.MouseMotionMsg:
		var cmd tea.Cmd
		m, cmd = m.handleMouseMotion(msg)
		cmds = append(cmds, cmd)

	case tea.MouseWheelMsg:
		var cmd tea.Cmd
		m, cmd = m.handleMouseWheel(msg)
		cmds = append(cmds, cmd)

	case engineReadyMsg:
		if msg.Err != nil {
			m.lifecycle.status = engineinit.StatusError
			m.lifecycle.err = msg.Err
			m.status = "engine error: " + msg.Err.Error()
			m.logger.Printf("engine open failed: %v", msg.Err)
		} else if m.lib != nil && msg.Engine != nil {
			m.lifecycle.status = engineinit.StatusReady
			m.engine = msg.Engine
			m.session = msg.Session
			m.status = "GPU ready · LLM commands enabled"
			m.logger.Printf("engine ready (hasMTP=%v)", msg.HasMTP)
		}

	case previewUpdatedMsg:
		m.renderingPreview = false
		if msg.ok && msg.img != nil {
			cmds = append(cmds, m.pic.SetImage(msg.img))
			m.logger.Printf("[PREVIEW] %s ok  bounds=%+v", msg.name, msg.img.Bounds())
		} else if msg.err != "" {
			m.lastErr = msg.err
			m.logger.Printf("[PREVIEW] %s err=%q", msg.name, msg.err)
		}
		if m.previewDirty {
			m.previewDirty = false
			m.renderingPreview = true
			cmds = append(cmds, m.refreshPreviewCmd())
		}

	case toolDoneMsg:
		m.inferencing = false
		m.genCh = nil
		m.genRound = 0
		if msg.err != nil {
			m.lastErr = msg.err.Error()
			m.toolHistory = append(m.toolHistory, "ERR: "+msg.err.Error())
			m.status = "error"
			m.logger.Printf("[DONE] error=%q", msg.err.Error())
		} else {
			m.toolHistory = append(m.toolHistory, msg.text)
			m.status = "tool complete"
			saved := ""
			if m.lastActiveLua != "" {
				saved = m.saveTimestampedLua()
			}
			if saved != "" {
				m.status += " · saved " + saved
			}
			m.logger.Printf("[DONE] text_len=%d reasoning_len=%d saved=%q objs=%d",
				len(msg.text), len(msg.reasoning), saved, len(m.w.Names()))
			// Auto-refresh preview when geometry was mutated.
			if len(m.w.Names()) > 0 {
				if cmd := m.refreshPreview(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		if len(m.toolHistory) > 12 {
			m.toolHistory = m.toolHistory[len(m.toolHistory)-12:]
		}
		if msg.reasoning != "" {
			m.lastThinking = strings.TrimSpace(msg.reasoning)
		}
		if msg.text != "" {
			m.lastLuaOutput = strings.TrimSpace(msg.text)
		}

	case camIdleMsg:
		// Camera input settled: replace the interactive low-res preview
		// with a full-resolution render. Stale ticks (camera moved again,
		// or a full-res render already happened) are dropped.
		if msg.seq == m.camSeq && m.previewLowRes {
			if cmd := m.refreshPreview(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case statusMsg:
		m.status = string(msg)

	case bubble.SpinnerTickMsg:
		if m.inferencing {
			m.spinnerFrame++
			cmds = append(cmds, spinnerTick())
		}

	case bubble.InferencingStartMsg:
		m.inferencing = true
		m.spinnerFrame = 0
		cmds = append(cmds, spinnerTick())

	case generationStartedMsg:
		m.genCh = msg.ch
		m.inferencing = true
		m.spinnerFrame = 0
		m.showThinking = true
		m.status = "generating with driver..."
		m.logger.Printf("[GEN] started")
		cmds = append(cmds, spinnerTick(), bubble.Wait(m.genCh))

	case driverEventMsg:
		m = m.handleDriverEvent(msg, &cmds)
		if m.genCh != nil {
			if !m.inferencing {
				m.inferencing = true
				m.spinnerFrame = 0
				cmds = append(cmds, spinnerTick())
			}
			cmds = append(cmds, bubble.Wait(m.genCh))
		}

	case kittyAutoToggleMsg:
		if picture.KittySupported() == picture.KittyCapabilitySupported && m.pic.Mode() == picture.PictureGlyph {
			cmds = append(cmds, m.pic.Toggle())
		}
	}

	if cmd := m.pic.Update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}

	// Auto-enable Kitty graphics when the terminal probe confirms support.
	if picture.KittySupported() == picture.KittyCapabilitySupported && m.pic.Mode() == picture.PictureGlyph {
		cmds = append(cmds, m.pic.Toggle())
	}

	return m, tea.Batch(cmds...)
}

func (m model) handleKeyMsg(msg tea.KeyMsg) (model, tea.Cmd) {
	// Log overlay is modal.
	if m.showLog {
		switch msg.String() {
		case "ctrl+c", "ctrl+q", "q":
			return m, tea.Quit
		case "esc":
			m.showLog = false
			m.logTop = -1
		case "up", "k":
			m.logTop = m.logScrollBy(-1)
		case "down", "j":
			m.logTop = m.logScrollBy(1)
		case "pgup":
			m.logTop = m.logScrollBy(-m.logPageSize())
		case "pgdown":
			m.logTop = m.logScrollBy(m.logPageSize())
		}
		return m, nil
	}

	// Source overlay is modal.
	if m.showSource {
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit
		case "esc", "v":
			m.showSource = false
		case "up", "k":
			m.sourceTop = m.sourceScrollBy(-1)
		case "down", "j":
			m.sourceTop = m.sourceScrollBy(1)
		case "pgup":
			m.sourceTop = m.sourceScrollBy(-m.sourcePageSize())
		case "pgdown":
			m.sourceTop = m.sourceScrollBy(m.sourcePageSize())
		}
		return m, nil
	}

	// Global quit / log toggle.
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c", "q"))):
		return m, tea.Quit
	case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+n"))):
		m.showLog = true
		return m, nil
	}

	// Help overlay is modal.
	if m.showHelp {
		if key.Matches(msg, key.NewBinding(key.WithKeys("esc", "?"))) {
			m.showHelp = false
		}
		return m, nil
	}

	// Edit mode (prompt focused).
	if m.input.Focused() {
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
			m.input.Blur()
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			if m.input.Value() != "" {
				cmd := m.submitInputCmd()
				m.input.Blur()
				return m, cmd
			}
			if names := m.w.Names(); len(names) > 0 {
				m.w.SetCurrent(names[m.selected])
				m.status = "current = " + names[m.selected]
				m.input.Blur()
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	// Command mode (prompt not focused).
	// Global keys work regardless of focus region.
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("e"))):
		return m, m.input.Focus()
	case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c", "q"))):
		return m, tea.Quit
	case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+n"))):
		m.showLog = true
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("h", "?"))):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
		m.cycleFocus()
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("t"))):
		switch m.thinkMode {
		case ds4.ThinkNone:
			m.thinkMode = ds4.ThinkHigh
		case ds4.ThinkHigh:
			m.thinkMode = ds4.ThinkMax
		default:
			m.thinkMode = ds4.ThinkNone
		}
		m.status = "reasoning: " + thinkModeLabel(m.thinkMode)
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("T"))):
		m.showThinking = !m.showThinking
		if !m.showThinking && m.focus == focusThinking {
			m.focus = focusViewport
		}
		state := "hidden"
		if m.showThinking {
			state = "shown"
		}
		m.status = "LLM output box " + state
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("v"))):
		m.toggleSourceView()
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("L"))):
		m.showLuaOutput = !m.showLuaOutput
		if !m.showLuaOutput && m.focus == focusLuaOutput {
			m.focus = focusViewport
		}
		state := "hidden"
		if m.showLuaOutput {
			state = "shown"
		}
		m.status = "lua output box " + state
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("s"))):
		_ = m.w.Save("cadpad-session.cad.json")
		m.logger.Printf("[SAVE] cadpad-session.cad.json")
		msg := "saved cadpad-session.cad.json"
		if saved := m.saveTimestampedLua(); saved != "" {
			msg += " · " + saved
			m.logger.Printf("[SAVE] %s", saved)
		}
		m.status = msg
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("j"))):
		m.moveSelection(1)
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("k"))):
		m.moveSelection(-1)
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		if names := m.w.Names(); len(names) > 0 {
			m.w.SetCurrent(names[m.selected])
			m.status = "current = " + names[m.selected]
		}
		return m, nil
	}

	// Focus-dependent keys.
	switch m.focus {
	case focusViewport:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("1"))):
			m.proj = render.ProjXY
			return m, m.refreshPreview()
		case key.Matches(msg, key.NewBinding(key.WithKeys("2"))):
			m.proj = render.ProjXZ
			return m, m.refreshPreview()
		case key.Matches(msg, key.NewBinding(key.WithKeys("3"))):
			m.proj = render.ProjYZ
			return m, m.refreshPreview()
		case key.Matches(msg, key.NewBinding(key.WithKeys("4"))):
			m.proj = render.ProjAngle
			return m, m.refreshPreview()
		case key.Matches(msg, key.NewBinding(key.WithKeys("]"))):
			m.cycleResolution(1)
			m.status = fmt.Sprintf("resolution: %d px", resPresets[m.resIndex])
			return m, m.refreshPreview()
		case key.Matches(msg, key.NewBinding(key.WithKeys("["))):
			m.cycleResolution(-1)
			m.status = fmt.Sprintf("resolution: %d px", resPresets[m.resIndex])
			return m, m.refreshPreview()
		case key.Matches(msg, key.NewBinding(key.WithKeys("left"))):
			if m.proj == render.ProjAngle {
				m.camAzimuth -= 0.2
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("right"))):
			if m.proj == render.ProjAngle {
				m.camAzimuth += 0.2
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("up"))):
			if m.proj == render.ProjAngle {
				m.camElevation += 0.2
				maxElev := float32(math.Pi/2 - 0.05)
				if m.camElevation > maxElev {
					m.camElevation = maxElev
				}
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("down"))):
			if m.proj == render.ProjAngle {
				m.camElevation -= 0.2
				maxElev := float32(math.Pi/2 - 0.05)
				if m.camElevation < -maxElev {
					m.camElevation = -maxElev
				}
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("shift+left"))):
			if m.proj == render.ProjAngle {
				m.camPanX -= 1.0
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("shift+right"))):
			if m.proj == render.ProjAngle {
				m.camPanX += 1.0
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("shift+up"))):
			if m.proj == render.ProjAngle {
				m.camPanY += 1.0
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("shift+down"))):
			if m.proj == render.ProjAngle {
				m.camPanY -= 1.0
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("+", "="))):
			if m.proj == render.ProjAngle {
				m.camZoom *= 0.9
				if m.camZoom < 0.1 {
					m.camZoom = 0.1
				}
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("-", "_"))):
			if m.proj == render.ProjAngle {
				m.camZoom *= 1.1
				if m.camZoom > 10.0 {
					m.camZoom = 10.0
				}
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("0"))):
			if m.proj == render.ProjAngle {
				m.camAzimuth = 0.61
				m.camElevation = 0.46
				m.camZoom = 1.0
				m.camPanX = 0
				m.camPanY = 0
				return m, m.refreshPreviewInteractive()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("p"))):
			return m, m.refreshPreview()
		case key.Matches(msg, key.NewBinding(key.WithKeys("r"))):
			m.renderer.ClearCache()
			return m, m.refreshPreview()
		case key.Matches(msg, key.NewBinding(key.WithKeys("pgup"))):
			if len(m.luaEntries) > 0 && m.luaEntryIndex > 0 {
				m.luaEntryIndex--
				if cmd := m.loadLuaEntryCmd(); cmd != nil {
					return m, cmd
				}
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("pgdown"))):
			if len(m.luaEntries) > 0 && m.luaEntryIndex < len(m.luaEntries)-1 {
				m.luaEntryIndex++
				if cmd := m.loadLuaEntryCmd(); cmd != nil {
					return m, cmd
				}
			}
		}
	case focusThinking:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("up"))):
			m.thinkScroll++
			m.status = fmt.Sprintf("thinking scroll %d", m.thinkScroll)
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("down"))):
			if m.thinkScroll > 0 {
				m.thinkScroll--
			}
			m.status = fmt.Sprintf("thinking scroll %d", m.thinkScroll)
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("pgup"))):
			m.thinkScroll += 5
			m.status = fmt.Sprintf("thinking scroll %d", m.thinkScroll)
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("pgdown"))):
			if m.thinkScroll > 5 {
				m.thinkScroll -= 5
			} else {
				m.thinkScroll = 0
			}
			m.status = fmt.Sprintf("thinking scroll %d", m.thinkScroll)
			return m, nil
		}
	case focusLuaOutput:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("up"))):
			m.luaScroll++
			m.status = fmt.Sprintf("lua scroll %d", m.luaScroll)
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("down"))):
			if m.luaScroll > 0 {
				m.luaScroll--
			}
			m.status = fmt.Sprintf("lua scroll %d", m.luaScroll)
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("pgup"))):
			m.luaScroll += 3
			m.status = fmt.Sprintf("lua scroll %d", m.luaScroll)
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("pgdown"))):
			if m.luaScroll > 3 {
				m.luaScroll -= 3
			} else {
				m.luaScroll = 0
			}
			m.status = fmt.Sprintf("lua scroll %d", m.luaScroll)
			return m, nil
		}
	}

	return m, nil
}

func (m *model) cycleFocus() {
	order := []focusRegion{focusViewport}
	if m.showThinking {
		order = append(order, focusThinking)
	}
	if m.showLuaOutput {
		order = append(order, focusLuaOutput)
	}
	idx := 0
	for i, r := range order {
		if r == m.focus {
			idx = i
			break
		}
	}
	m.focus = order[(idx+1)%len(order)]
	switch m.focus {
	case focusViewport:
		m.status = "focus: viewport"
	case focusThinking:
		m.status = "focus: LLM output"
	case focusLuaOutput:
		m.status = "focus: lua output"
	}
}

func (m model) handleDriverEvent(msg driverEventMsg, cmds *[]tea.Cmd) model {
	if msg.e == nil {
		return m
	}
	switch ev := msg.e.(type) {
	case bubble.LogEvent:
		entry := fmt.Sprintf("[%s] %s", ev.Level, ev.Message)
		m.generationLog = append(m.generationLog, entry)
		if len(m.generationLog) > 60 {
			m.generationLog = m.generationLog[len(m.generationLog)-60:]
		}
		m.logger.Printf("[DRIVER] %s", entry)
		if p := extractLuaPath(ev.Message); p != "" {
			if !filepath.IsAbs(p) && m.luaWorkspace != "" {
				p = filepath.Join(m.luaWorkspace, p)
			}
			m.lastActiveLua = p
		}
		if strings.HasPrefix(ev.Message, "tool:") {
			m.toolHistory = append(m.toolHistory, ev.Message)
			if len(m.toolHistory) > 12 {
				m.toolHistory = m.toolHistory[len(m.toolHistory)-12:]
			}
			m.status = ev.Message
		} else if strings.Contains(ev.Message, "executing") {
			m.status = ev.Message
		}

	case bubble.TokenEvent:
		// Too noisy for file log; keep in generationLog only if needed.

	case bubble.AssistantMessageEvent:
		m.logger.Printf("[ASSISTANT] role=%s content_len=%d reasoning_len=%d", ev.Message.Role, len(ev.Message.Content), len(ev.Message.ReasoningContent))
		reasoning := ev.Message.ReasoningContent
		if reasoning == "" {
			reasoning, _ = extractThinkFromContent(ev.Message.Content)
		}
		if reasoning != "" {
			label := "THINK"
			if m.genRound > 0 {
				label = fmt.Sprintf("THINK r%d", m.genRound)
			}
			entry := fmt.Sprintf("[%s] %s", label, strings.TrimSpace(reasoning))
			m.reasoningLog = append(m.reasoningLog, entry)
			if len(m.reasoningLog) > 20 {
				m.reasoningLog = m.reasoningLog[len(m.reasoningLog)-20:]
			}
		}

	case bubble.ToolCallsEvent:
		for _, c := range ev.Calls {
			m.logger.Printf("[TOOL] call %s args=%.200q", c.Name, c.Arguments)
		}

	case bubble.ToolResultsEvent:
		for _, r := range ev.Results {
			m.logger.Printf("[TOOL] result role=%s content_len=%d", r.Role, len(r.Content))
		}

	case bubble.RoundStartedEvent:
		m.genRound = ev.Round + 1
		m.generationLog = append(m.generationLog, fmt.Sprintf("── round %d ──", m.genRound))
		if len(m.generationLog) > 60 {
			m.generationLog = m.generationLog[len(m.generationLog)-60:]
		}
		m.status = fmt.Sprintf("generating (round %d)", m.genRound)
		m.logger.Printf("[ROUND] started %d", m.genRound)

	case bubble.RoundCompletedEvent:
		m.logger.Printf("[ROUND] completed %d", ev.Round+1)

	case bubble.ErrorEvent:
		if ev.Err != nil {
			m.lastErr = ev.Err.Error()
			m.generationLog = append(m.generationLog, "[ERROR] "+ev.Err.Error())
			m.logger.Printf("[ERROR] %v", ev.Err)
		}
	}
	return m
}

func (m model) viewportBounds() (x0, y0, x1, y1 int) {
	listW := max(minListW, m.width/5)
	propsW := max(minPropsW, m.width/5)
	viewW := max(minViewW, m.width-listW-propsW-4)
	h := m.bodyH()
	return listW, 1, listW + viewW - 1, 1 + h - 1
}

func (m model) handleMouseClick(msg tea.MouseClickMsg) (model, tea.Cmd) {
	if m.proj != render.ProjAngle {
		return m, nil
	}

	x0, y0, x1, y1 := m.viewportBounds()
	if msg.X >= x0 && msg.X <= x1 && msg.Y >= y0 && msg.Y <= y1 {
		m.focus = focusViewport
		m.mouseDragging = true
		m.lastMouseX = msg.X
		m.lastMouseY = msg.Y
	}
	return m, nil
}

func (m model) handleMouseRelease(msg tea.MouseReleaseMsg) (model, tea.Cmd) {
	m.mouseDragging = false
	return m, nil
}

func (m model) handleMouseMotion(msg tea.MouseMotionMsg) (model, tea.Cmd) {
	if !m.mouseDragging {
		return m, nil
	}

	dx := msg.X - m.lastMouseX
	dy := msg.Y - m.lastMouseY

	// Update last coordinates
	m.lastMouseX = msg.X
	m.lastMouseY = msg.Y

	if dx == 0 && dy == 0 {
		return m, nil
	}

	// Check modifier
	if msg.Mod&tea.ModShift != 0 {
		// Panning mode: shift + drag
		m.camPanX -= float32(dx) * 0.25
		m.camPanY -= float32(dy) * 0.25 // In terminal, Y goes down, but camera panning Y is up
	} else {
		// Orbit mode: drag
		m.camAzimuth -= float32(dx) * 0.05
		m.camElevation -= float32(dy) * 0.03
		maxElev := float32(math.Pi/2 - 0.05)
		if m.camElevation > maxElev {
			m.camElevation = maxElev
		}
		if m.camElevation < -maxElev {
			m.camElevation = -maxElev
		}
	}

	return m, m.refreshPreviewInteractive()
}

func (m model) handleMouseWheel(msg tea.MouseWheelMsg) (model, tea.Cmd) {
	if m.proj != render.ProjAngle {
		return m, nil
	}

	x0, y0, x1, y1 := m.viewportBounds()
	if msg.X >= x0 && msg.X <= x1 && msg.Y >= y0 && msg.Y <= y1 {
		if msg.Button == tea.MouseWheelUp {
			m.camZoom *= 0.9
			if m.camZoom < 0.1 {
				m.camZoom = 0.1
			}
			return m, m.refreshPreviewInteractive()
		} else if msg.Button == tea.MouseWheelDown {
			m.camZoom *= 1.1
			if m.camZoom > 10.0 {
				m.camZoom = 10.0
			}
			return m, m.refreshPreviewInteractive()
		}
	}
	return m, nil
}

// camIdleDelay is how long camera input must be quiet before the low-res
// interactive preview is replaced by a full-resolution render.
const camIdleDelay = 200 * time.Millisecond

// refreshPreview requests a full-resolution preview render (coalescing with
// any render already in flight).
func (m *model) refreshPreview() tea.Cmd {
	m.previewLowRes = false
	return m.requestPreview()
}

// refreshPreviewInteractive requests a reduced-resolution preview for a
// camera movement — sphere tracing cost scales with pixel count, so the
// low-res pass keeps orbit/zoom/pan responsive — and arms an idle timer
// that triggers the full-resolution render once input settles.
func (m *model) refreshPreviewInteractive() tea.Cmd {
	m.previewLowRes = true
	m.camSeq++
	seq := m.camSeq
	idle := tea.Tick(camIdleDelay, func(time.Time) tea.Msg { return camIdleMsg{seq: seq} })
	if cmd := m.requestPreview(); cmd != nil {
		return tea.Batch(cmd, idle)
	}
	return idle
}

func (m *model) requestPreview() tea.Cmd {
	if m.renderingPreview {
		m.previewDirty = true
		return nil
	}
	m.renderingPreview = true
	return m.refreshPreviewCmd()
}

func (m model) clearViewCache() {
	if m.cachedView != nil {
		*m.cachedView = ""
	}
}
