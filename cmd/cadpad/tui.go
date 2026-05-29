// Package main (cadpad) - Bubble Tea TUI implementation.
// Layout (flexible via lipgloss):
//   header (engine badge + world stats)
//   body: [ left objects list | main picture viewport + proj tabs | right props/bbox ]
//   footer: LLM command input + last tool results scroll
//
// The picture.Model receives image.Image updates from world.RenderPreview
// tool calls (or manual 'p' refresh). Previews are cheap midplane slices.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/harness"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/tools"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/NimbleMarkets/ds4go-apps/internal/editmode"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/headerbar"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
	"github.com/charmbracelet/x/ansi"
	simplesdf "github.com/soypat/gsdf/gsdfaux/simplesdf"
)

const (
	headerH   = 1
	helpH     = 1
	footerH   = 3 // input + status (help is now separate)
	minListW  = 18
	minPropsW = 22
	minViewW  = 30
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	objStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	currStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// messages
type engineReadyMsg engineinit.Result
type previewUpdatedMsg struct {
	name string
	ok   bool
	err  string
}
type toolDoneMsg struct {
	text      string
	err       error
	reasoning string // model's thinking/reasoning content for this turn (if any)
}

type spinnerTickMsg struct{}
type inferencingStartMsg struct{}
type inferencingDoneMsg struct{}

type toolProgressMsg struct {
	text      string
	reasoning string
}
type statusMsg string

type toolCallEntry struct {
	round  int
	name   string
	args   string
	result string
}

// driverEventMsg bridges GenerationDriver events back into the Bubble Tea
// update loop so that generationLog, luaRuntimeLog, and tool metrics are
// populated live during inference.
type driverEventMsg struct {
	e bubble.Event
}

// generationStartedMsg is sent when a driver-powered LLM generation
// has been launched. It installs the receive channel so subsequent
// driver events can be delivered.
type generationStartedMsg struct {
	ch chan tea.Msg
}

// model is the Bubble Tea root.
type model struct {
	// core state
	w          *world.World
	renderer   *render.Renderer
	proj       render.Projection
	selected   int // index into w.Names()

	// LLM / engine
	lib        *ds4.Library
	engOpts    ds4.EngineOptions
	engine     *ds4.Engine
	session    *ds4.Session
	tools      *ds4.ToolRegistry
	thinkMode  ds4.ThinkMode
	thinking   bool
	maxRounds  int

	// UI widgets
	pic        picture.Model
	input      textinput.Model
	logBuf     *ds4log.Buffer
	logger     *log.Logger

	// layout & status
	width, height int
	status        string
	lastErr       string
	toolHistory   []string // recent tool results (bottom pane)
	showHelp      bool
	showLog       bool // libds4 diagnostic overlay (ctrl+n), works even in --no-engine mode
	logTop        int  // -1 = follow tail in the log overlay

	// Toggleable contained boxes (for limited screen space)
	showThinking  bool
	showLuaOutput bool
	showModelInfo bool

	// High-level session logs shown alongside engine logs in the ctrl+n overlay.
	// This includes model reasoning (thinking) so users can see *why* the LLM
	// chose certain CAD operations.
	reasoningLog []string // bounded recent model thinking / reasoning blocks

	// Latest model (LLM) output, displayed in a contained box in the right panel.
	modelOutput string

	// Dedicated content for the toggleable boxes
	lastThinking  string // latest reasoning block for the thinking box
	lastLuaOutput string // latest result from lua_run or Lua execution

	// Inferencing state (for live indicator + intermediate tool output)
	inferencing  bool
	spinnerFrame int

	// lifecycle
	lifecycle  engineLifecycle
	ctxSize    int
	modelPath  string
	backend    string
	debug      bool

	// Lua workspace + most recent script touched by the LLM.
	// Used for automatic time-dated .lua exports (mirrors svgpad's
	// time-dated .svg behavior on successful generation).
	luaWorkspace  string
	lastActiveLua string

	// Active driver generation channel (for multi-event streaming from RunWithPrompt).
	// We re-arm bubble.Wait(genCh) on every driverEventMsg.
	genCh chan tea.Msg

	// Rich generation tracing and Lua-specific logs (populated live by driver events)
	generationLog []string
	luaRuntimeLog []string

	// Tool call metrics (populated live during generation)
	toolCalls      []toolCallEntry
	toolRounds     int
	totalToolCalls int
	toolCallCounts map[string]int
}

type engineLifecycle struct {
	status engineinit.Status
	err    error
}

func newModel(lib *ds4.Library, engOpts ds4.EngineOptions, ctxSize int, modelPath, backend string, logger *log.Logger, logBuf *ds4log.Buffer, debug bool) model {
	w := world.NewWorld()
	rend, _ := render.NewRenderer(render.DefaultPreviewConfig)

	ti := textinput.New()
	ti.Placeholder = "describe what to build or /help for commands... (press 'e' to edit)"
	ti.SetWidth(60)
	ti.Prompt = "> "

	pic := picture.NewWithConfig(picture.Config{
		CellPixelWidth:  8,
		CellPixelHeight: 16,
		Fit:             picture.FitContain,
	})

	m := model{
		w:          w,
		renderer:   rend,
		proj:       render.ProjXY,
		lib:        lib,
		engOpts:    engOpts,
		ctxSize:    ctxSize,
		modelPath:  modelPath,
		backend:    backend,
		logger:     logger,
		logBuf:     logBuf,
		debug:      debug,
		input:      ti,
		pic:        pic,
		tools:      ds4.NewToolRegistry(),
		thinkMode:  ds4.ThinkNone,
		maxRounds:  6,
		lifecycle:  engineLifecycle{status: engineinit.StatusDormant},
		status:     "Ready. Type a modeling request or /create box base 4 3 2",
		logTop:     -1, // follow tail for ctrl+n libds4 log overlay
	}

	// Tweak the welcome text for the common --no-engine / harness case.
	if lib == nil {
		m.status = "geometry only (no LLM engine). Slash commands + previews work; natural language needs a real model."
	}

	// Seed a couple of demo objects so the TUI is immediately useful.
	seedDemo(m.w)

	// Register cadpad tools into the registry (harness wires the live World+Renderer).
	harness.MustRegisterAll(m.tools, m.w, m.renderer)

	// New Lua-first tools (primary interface). The LLM builds models by writing
	// and running Lua programs using low-level sdf primitives.
	workspace := filepath.Join(mustUserHome(), ".cadpad", "lua-workspace")
	if err := tools.RegisterLuaFileTools(m.tools, tools.LuaFileTools{
		Workspace: workspace,
		W:         m.w,
		R:         m.renderer,
	}); err != nil {
		// Non-fatal in first pass — log it.
		logger.Printf("warning: failed to register lua file tools: %v", err)
	}

	m.luaWorkspace = workspace

	return m
}

func mustUserHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func seedDemo(w *world.World) {
	w.Create("base", "box", map[string]float64{"x": 8, "y": 6, "z": 1.5})
	w.Create("post", "cylinder", map[string]float64{"r": 0.8, "h": 4})
	w.Transform("post", "translate", map[string]float64{"x": 0, "y": 0, "z": 2.75})
	w.Boolean("union", "base", "post", 0.2)
	w.Create("hole", "cylinder", map[string]float64{"r": 0.4, "h": 3})
	w.Transform("hole", "translate", map[string]float64{"x": 2.5, "y": 0, "z": 0})
	w.Boolean("diff", "base", "hole", 0)
	w.SetCurrent("base")
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.pic.Init(),
		picture.RequestCellSize(),
		picture.QueryKittySupport(),
		tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
			return previewUpdatedMsg{} // initial paint trigger
		}),
	}

	// Only attempt to open the ds4 engine when we actually have a library.
	// --no-engine (pure geometry mode) leaves lib==nil; we must never call
	// engineinit.Open(nil) or the TUI will panic (or get a spurious error).
	if m.lib != nil {
		cmds = append(cmds, func() tea.Msg {
			res := engineinit.Open(m.lib, m.engOpts, m.ctxSize)
			return engineReadyMsg(res)
		})
	}
	// When lib==nil we simply stay in StatusDormant ("💤 idle" badge) which
	// is the correct state for pure geometry / harness embedding use.

	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m = m.resize()
		cmds = append(cmds, m.pic.SetSize(m.viewW(), m.viewH()))

	case tea.KeyMsg:
		// Log overlay is a modal scrollable view (like in svgpad/glyphpad).
		// It works even in --no-engine mode because the logBuf is always installed.
		if m.showLog {
			switch msg.String() {
			case "ctrl+c", "ctrl+q", "q":
				return m, tea.Quit
			case "esc":
				m.showLog = false
				m.logTop = -1 // resume follow-tail next time
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

		// Global keys that always work (even in modals or when input focused).
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c", "q"))):
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+n"))):
			m.showLog = true
			return m, nil
		}

		// Log and help overlays are fully modal.
		if m.showHelp {
			if key.Matches(msg, key.NewBinding(key.WithKeys("esc", "?"))) {
				m.showHelp = false
			}
			return m, nil
		}

		// ── Prompt window modes (unified with glyphpad/svgpad via editmode) ──
		// Edit mode: the prompt box is focused (targeted). Most keys type.
		// Command mode: box is not focused. Bare letters are commands.
		if m.input.Focused() {
			// Edit mode for the CAD/LLM prompt.
			switch {
			case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
				m.input.Blur()
				return m, nil
			case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
				if m.input.Value() != "" {
					cmds = append(cmds, m.submitInputCmd())
					// After submit, return to command mode (standard pattern).
					m.input.Blur()
				} else if names := m.w.Names(); len(names) > 0 {
					m.w.SetCurrent(names[m.selected])
					m.status = "current = " + names[m.selected]
					m.input.Blur()
				}
				return m, tea.Batch(cmds...)
			}
			// Everything else (including letters, arrows, etc.) goes to the textinput.
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		// Command mode (prompt box not targeted). Bare-letter commands + navigation.
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
			// In command mode esc is usually a no-op (or could clear selection later).
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("e"))):
			// Enter edit mode for the prompt window (standard unification key).
			cmds = append(cmds, m.input.Focus())
			return m, tea.Batch(cmds...)

		case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
			m.cycleProjection()
			cmds = append(cmds, m.refreshPreviewCmd())

		case key.Matches(msg, key.NewBinding(key.WithKeys("1"))):
			m.proj = render.ProjXY
			cmds = append(cmds, m.refreshPreviewCmd())
		case key.Matches(msg, key.NewBinding(key.WithKeys("2"))):
			m.proj = render.ProjXZ
			cmds = append(cmds, m.refreshPreviewCmd())
		case key.Matches(msg, key.NewBinding(key.WithKeys("3"))):
			m.proj = render.ProjYZ
			cmds = append(cmds, m.refreshPreviewCmd())

		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			m.moveSelection(1)
		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			m.moveSelection(-1)

		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			// In command mode with empty box: set current object (convenience).
			if names := m.w.Names(); len(names) > 0 {
				m.w.SetCurrent(names[m.selected])
				m.status = "current = " + names[m.selected]
			}

		case key.Matches(msg, key.NewBinding(key.WithKeys("p"))):
			cmds = append(cmds, m.refreshPreviewCmd())
		case key.Matches(msg, key.NewBinding(key.WithKeys("r"))):
			m.renderer.ClearCache()
			cmds = append(cmds, m.refreshPreviewCmd())
		case key.Matches(msg, key.NewBinding(key.WithKeys("s"))):
			_ = m.w.Save("cadpad-session.cad.json")
			msg := "saved cadpad-session.cad.json"
			if saved := m.saveTimestampedLua(); saved != "" {
				msg += " · " + saved
			}
			m.status = msg

		case key.Matches(msg, key.NewBinding(key.WithKeys("h", "?"))):
			m.showHelp = !m.showHelp

		case key.Matches(msg, key.NewBinding(key.WithKeys("t"))):
			// Cycle thinking / reasoning effort (routed into the ctrl+n log box)
			switch m.thinkMode {
			case ds4.ThinkNone:
				m.thinkMode = ds4.ThinkHigh
			case ds4.ThinkHigh:
				m.thinkMode = ds4.ThinkMax
			default:
				m.thinkMode = ds4.ThinkNone
			}
			m.status = "reasoning: " + thinkModeLabel(m.thinkMode)

		case key.Matches(msg, key.NewBinding(key.WithKeys("T"))):
			m.showThinking = !m.showThinking
			if m.showThinking && m.lastThinking != "" {
				m.status = "showing thinking box"
			}

		case key.Matches(msg, key.NewBinding(key.WithKeys("L"))):
			m.showLuaOutput = !m.showLuaOutput
			if m.showLuaOutput && m.lastLuaOutput != "" {
				m.status = "showing lua output box"
			}
		}

		// In command mode we do *not* forward random keys to the input.

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
		// lib==nil (pure geometry / --no-engine): leave status as Dormant.
		// The badge will correctly show the idle pill and LLM path will
		// gracefully degrade to slash-command echo mode.

	case previewUpdatedMsg:
		// Pull latest from world and feed picture widget if successful.
		name, proj, iw, ih, ok, e, _ := m.w.GetPreview()
		if ok && name != "" {
			// Re-render at current view size for crispness (renderer caches the SDF2 wrapper).
			s, _, _ := m.w.Get(name)
			if s.Shader() != nil {
				img, _, _ := m.renderer.Render(s, name, render.Projection(proj), m.viewW()*8, m.viewH()*16)
				if img != nil {
					cmds = append(cmds, m.pic.SetImage(img))
				}
			}
		}
		if e != "" {
			m.lastErr = e
		}
		_ = iw
		_ = ih

	case toolDoneMsg:
		if msg.err != nil {
			m.lastErr = msg.err.Error()
			m.toolHistory = append(m.toolHistory, "ERR: "+msg.err.Error())
		} else {
			m.toolHistory = append(m.toolHistory, msg.text)
		}
		if len(m.toolHistory) > 12 {
			m.toolHistory = m.toolHistory[len(m.toolHistory)-12:]
		}

		// Route model thinking/reasoning into the log box (ctrl+n), alongside
		// the engine (libds4) logs from logBuf.
		if msg.reasoning != "" {
			entry := fmt.Sprintf("[THINK %s] %s", thinkModeLabel(m.thinkMode), strings.TrimSpace(msg.reasoning))
			m.reasoningLog = append(m.reasoningLog, entry)
			if len(m.reasoningLog) > 20 {
				m.reasoningLog = m.reasoningLog[len(m.reasoningLog)-20:]
			}
			m.lastThinking = strings.TrimSpace(msg.reasoning)
		}

		// Capture the full final model output (the "whole lot of output" after inference)
		// into the dedicated toggleable boxes (Thinking + Lua Output).
		if msg.text != "" {
			// The main final response goes to the Lua Output box
			m.lastLuaOutput = strings.TrimSpace(msg.text)

			// Also keep a combined view for the right panel (legacy)
			out := msg.text
			if msg.reasoning != "" {
				out = "Reasoning (" + thinkModeLabel(m.thinkMode) + "):\n" + strings.TrimSpace(msg.reasoning) + "\n\n" + msg.text
			}
			m.modelOutput = strings.TrimSpace(out)
		}

		m.status = "tool complete"

	case toolProgressMsg:
		// Live tool output during inference (not just at the very end)
		if msg.reasoning != "" {
			m.lastThinking = strings.TrimSpace(msg.reasoning)
		}
		if msg.text != "" {
			// Prefer showing in the Lua box if it looks like Lua/tool output
			if strings.Contains(msg.text, "lua") || strings.Contains(msg.text, "Executed") || strings.Contains(msg.text, "sdf.") {
				m.lastLuaOutput = msg.text
			} else {
				m.lastThinking = m.lastThinking + "\n\n" + msg.text
			}
		}
		// Keep the robot spinning
		if !m.inferencing {
			m.inferencing = true
			cmds = append(cmds, spinnerTick())
		}
		// After tool, auto-refresh preview of current.
		cmds = append(cmds, m.refreshPreviewCmd())

		// Inference has finished (stop the robot indicator)
		if m.inferencing {
			cmds = append(cmds, func() tea.Msg { return inferencingDoneMsg{} })
		}

	case statusMsg:
		m.status = string(msg)

	case spinnerTickMsg:
		if m.inferencing {
			m.spinnerFrame++
			cmds = append(cmds, spinnerTick())
		}

	case inferencingStartMsg:
		m.inferencing = true
		m.spinnerFrame = 0
		cmds = append(cmds, spinnerTick())

	case generationStartedMsg:
		m.genCh = msg.ch
		m.inferencing = true
		m.spinnerFrame = 0
		// Auto-open the Generation Log so the user can watch the driver work.
		m.showThinking = true
		m.status = "generating with driver..."
		cmds = append(cmds, spinnerTick(), bubble.Wait(m.genCh))

	case driverEventMsg:
		m.handleDriverEvent(msg.e, &cmds)
		// Keep robot + generation alive while driver events are flowing.
		if m.genCh != nil {
			if !m.inferencing {
				m.inferencing = true
				m.spinnerFrame = 0
				cmds = append(cmds, spinnerTick())
			}
			cmds = append(cmds, bubble.Wait(m.genCh))
		}

	case inferencingDoneMsg:
		m.inferencing = false
		m.genCh = nil
	}

	// Keep pic widget alive (v2 Update returns only the cmd).
	cmd := m.pic.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() tea.View {
	if m.width == 0 {
		return tea.NewView("initializing...")
	}
	if m.showLog {
		return tea.NewView(m.logOverlay())
	}
	if m.showHelp {
		return tea.NewView(m.helpView())
	}

	hdr := m.header()
	body := m.bodyView()           // geometry (height adjusted for boxes)
	boxes := m.bottomBoxesView()   // thinking + lua output (when toggled)
	foot := m.footerView()
	help := m.helpLine()           // dedicated help strip at the very bottom

	return tea.NewView(lipgloss.JoinVertical(lipgloss.Left, hdr, body, boxes, foot, help))
}

func (m model) header() string {
	// Top header line as requested: "nm cadpad" + primary information
	// including kitty graphics status and GPU/engine state.

	brand := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Render("nm cadpad")

	var info []string

	// Current world info
	if cur := m.w.Current(); cur != "" {
		info = append(info, cur)
	}
	info = append(info, fmt.Sprintf("%d objs", len(m.w.Names())))

	// Think mode (if active)
	if m.thinkMode != ds4.ThinkNone {
		info = append(info, "think:"+thinkModeLabel(m.thinkMode))
	}

	// Kitty graphics capability
	kitty := m.pic.KittySupported()
	var kittyBadge string
	switch kitty {
	case picture.KittyCapabilitySupported:
		kittyBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("Kitty ✓")
	case picture.KittyCapabilityUnsupported:
		kittyBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("glyph")
	default:
		kittyBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("Kitty ?")
	}
	info = append(info, kittyBadge)

	// GPU / engine state badge (reusing existing engineinit)
	gpuBadge := ""
	if m.engine != nil {
		gpuBadge = engineinit.Badge(engineinit.StatusReady)
	} else if m.lifecycle.status == engineinit.StatusError {
		gpuBadge = engineinit.Badge(engineinit.StatusError)
	} else if m.inferencing {
		gpuBadge = engineinit.Badge(engineinit.StatusOpening)
	} else {
		gpuBadge = engineinit.Badge(engineinit.StatusDormant)
	}

	left := brand + "  " + strings.Join(info, "  ")

	// Show the robot animation in the header when the model is thinking/generating
	status := ""
	if m.inferencing {
		status = m.robotSpinner()
	}

	raw := headerbar.Layout(m.width, left, status, "", gpuBadge)

	// Make the header stand out with a contrasting background bar
	return lipgloss.NewStyle().
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("252")).
		Width(m.width).
		Render(raw)
}

func (m model) bodyView() string {
	listW := max(minListW, m.width/5)
	propsW := max(minPropsW, m.width/5)
	viewW := max(minViewW, m.width-listW-propsW-4)

	list := m.objectsList(listW)
	view := m.viewportView(viewW)
	props := m.propsView(propsW)

	return lipgloss.JoinHorizontal(lipgloss.Top, list, view, props)
}

func (m model) objectsList(w int) string {
	names := m.w.Names()
	if len(names) == 0 {
		return dimStyle.Render("  (no objects)\n  /create or describe")
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(" Objects\n"))
	for i, n := range names {
		style := objStyle
		if i == m.selected || n == m.w.Current() {
			style = currStyle
		}
		pfx := "  "
		if n == m.w.Current() {
			pfx = "▶ "
		}
		line := pfx + n
		b.WriteString(style.Width(w - 2).Render(line) + "\n")
	}
	return lipgloss.NewStyle().Width(w).MaxHeight(m.bodyH()).Render(b.String())
}

func (m model) viewportView(w int) string {
	// picture + small header for projection + name
	projLabel := strings.ToUpper(string(m.proj))
	cur := m.w.Current()
	if cur == "" {
		cur = "(none)"
	}
	header := fmt.Sprintf("%s view · %s  (tab/1/2/3 p r)", projLabel, cur)
	v := m.pic.View()
	// The picture view already includes its own border handling in most cases.
	content := ""
	if v.Content != "" {
		content = v.Content
	}
	return lipgloss.NewStyle().
		Width(w).
		Height(m.bodyH()).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Render(lipgloss.JoinVertical(lipgloss.Left, dimStyle.Render(header), content))
}

func (m model) propsView(w int) string {
	cur := m.w.Current()
	if cur == "" {
		return dimStyle.Width(w).Render(" no selection\n\nj/k: nav\nenter: set current\np: preview\ns: save (json + .lua)\nt: cycle think mode\nT: toggle thinking box\nL: toggle lua output box\nctrl+n: full logs\n?: help")
	}
	s, meta, ok := m.w.Get(cur)
	if !ok {
		return errStyle.Render("missing?")
	}
	bb, _ := m.w.Bounds(cur)
	// Very lightweight: show bbox + rough volume.
	vol := (bb.Max.X - bb.Min.X) * (bb.Max.Y - bb.Min.Y) * (bb.Max.Z - bb.Min.Z)
	txt := fmt.Sprintf("%s\n\nbbox:\n  x %.2f..%.2f\n  y %.2f..%.2f\n  z %.2f..%.2f\nvol≈%.1f\n\nupdated: %s",
		cur,
		bb.Min.X, bb.Max.X,
		bb.Min.Y, bb.Max.Y,
		bb.Min.Z, bb.Max.Z,
		vol,
		meta.Updated.Format("15:04:05"),
	)
	_ = s

	// Contained "Model Output" box inside the right props panel (not an overlay).
	// Shows the latest LLM response + reasoning so the user can see what the
	// model just did/said while looking at the affected object.
	if m.modelOutput != "" {
		// Allocate some space in the right column for the output box.
		outH := 6
		avail := m.bodyH() - 12 // leave room for bbox + header
		if avail > 4 {
			outH = min(avail, 8)
		}

		boxContent := lipgloss.NewStyle().
			Width(w - 4).
			MaxHeight(outH - 2).
			Render(m.modelOutput)

		outBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("12")).
			Width(w).
			MaxHeight(outH).
			Padding(0, 1).
			Render(lipgloss.JoinVertical(lipgloss.Left,
				dimStyle.Render("Model Output"),
				boxContent,
			))

		txt += "\n\n" + outBox
	}

	return lipgloss.NewStyle().Width(w).MaxHeight(m.bodyH()).Render(txt)
}

func (m model) bottomBoxesView() string {
	if !m.showThinking && !m.showLuaOutput {
		return ""
	}

	w := m.width
	var parts []string

	if m.showThinking {
		content := m.lastThinking
		if content == "" {
			content = dimStyle.Render("(no thinking output yet)")
		}
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("13")).
			Width(w).
			MaxHeight(6).
			Padding(0, 1).
			Render(lipgloss.JoinVertical(lipgloss.Left,
				"Thinking (toggle with T)",
				content,
			))
		parts = append(parts, box)
	}

	if m.showLuaOutput {
		content := m.lastLuaOutput
		if content == "" {
			content = dimStyle.Render("(no lua output yet — use lua_run)")
		}
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("10")).
			Width(w).
			MaxHeight(6).
			Padding(0, 1).
			Render(lipgloss.JoinVertical(lipgloss.Left,
				"Lua Output (toggle with L)",
				content,
			))
		parts = append(parts, box)
	}

	return strings.Join(parts, "\n")
}

func (m model) robotSpinner() string {
	robot := "🤖"
	track := 12
	cycle := track * 2
	p := m.spinnerFrame % cycle
	if p > track {
		p = cycle - p
	}
	left := strings.Repeat(" ", p)
	right := strings.Repeat(" ", track-p)
	spinner := "[" + left + robot + right + "]"

	return lipgloss.NewStyle().
		Background(lipgloss.Color("63")). // nice robot blue/purple
		Foreground(lipgloss.Color("15")).
		Bold(true).
		Render(spinner)
}

func (m model) helpLine() string {
	// Dedicated help line at the very bottom, as requested.
	return dimStyle.Render(" " + m.keymap().FooterText(m.input.Focused()))
}

func (m model) footerView() string {
	inputView := m.input.View()
	hist := ""
	if len(m.toolHistory) > 0 {
		hist = dimStyle.Render(" " + strings.Join(m.toolHistory[max(0, len(m.toolHistory)-2):], " | "))
	}
	errLine := ""
	if m.lastErr != "" {
		errLine = errStyle.Render(" " + ansi.Truncate(m.lastErr, m.width-2, "…"))
	}
	status := okStyle.Render(m.status)
	if m.lifecycle.status == engineinit.StatusError {
		status = errStyle.Render("engine: " + m.lifecycle.err.Error())
	}

	line := status + hist

	return lipgloss.JoinVertical(lipgloss.Left,
		inputView,
		lipgloss.NewStyle().Width(m.width).Render(line),
		errLine,
	)
}

func (m model) helpView() string {
	help := `cadpad — LLM CAD scratchpad

Keys
  e             edit / target the prompt window (command → edit mode)
  esc           blur prompt window (edit → command mode)
  enter         submit LLM command (in edit mode) or set current (command mode)
  j/k ↑/↓       navigate object list (command mode)
  tab / 1 2 3   cycle XY / XZ / YZ projection (command mode)
  p             refresh preview of current
  r             clear render cache + refresh
  s             quick-save history to cadpad-session.cad.json
  t             cycle reasoning effort (OFF / HIGH / MAX)
  T             toggle thinking output box (contained)
  L             toggle lua output box (contained)
  ctrl+n        show/hide full log overlay (engine + thinking history)
  While the robot 🤖 is moving in the status line, the model + tools are working.
  q / ctrl-c    quit
  ? / h         toggle this help

The bottom prompt box follows the same Command/Edit mode model as glyphpad and svgpad
(using the shared editmode package). Bare letters are commands unless the box is targeted.

Commands (bottom bar)
  Plain text is sent to the LLM with full cad_* tool suite.
  Slash commands work without LLM:
    /create sphere ball r=2.5
    /boolean diff base cutter blend=0.1
    /transform post translate 0 0 3
    /group assembly base post
    /export name.stl
    /save mysession.cad.json
    /load mysession.cad.json
    /clear

LLM tips
  "create a lua script that builds a plate with an array of holes using a loop"
  "write a reusable function for rounded posts and use it"
  "use lua_run to execute your script and see the result in the viewport"

The world is a live replayable history. Save often.`
	return lipgloss.NewStyle().Padding(1, 2).Render(help)
}

func (m model) resize() model {
	m.input.SetWidth(max(20, m.width-4))
	return m
}

func (m model) bodyH() int {
	extra := m.bottomBoxesHeight()
	return max(8, m.height-headerH-helpH-footerH-2-extra)
}

func (m model) bottomBoxesHeight() int {
	if !m.showThinking && !m.showLuaOutput {
		return 0
	}
	h := 0
	if m.showThinking {
		h += 7 // title + content + border/padding
	}
	if m.showLuaOutput {
		h += 7
	}
	if m.showThinking && m.showLuaOutput {
		h += 1 // small separator
	}
	return h
}

func (m model) viewW() int {
	return max(20, m.width/2)
}

func (m model) viewH() int {
	return max(8, m.bodyH()-3)
}

func (m model) cycleProjection() {
	switch m.proj {
	case render.ProjXY:
		m.proj = render.ProjXZ
	case render.ProjXZ:
		m.proj = render.ProjYZ
	default:
		m.proj = render.ProjXY
	}
}

func (m model) moveSelection(delta int) {
	names := m.w.Names()
	if len(names) == 0 {
		return
	}
	m.selected = (m.selected + delta + len(names)) % len(names)
}

func (m model) refreshPreviewCmd() tea.Cmd {
	cur := m.w.Current()
	if cur == "" {
		names := m.w.Names()
		if len(names) == 0 {
			return nil
		}
		cur = names[0]
	}
	// Fire a tool-like call that updates world preview state.
	return func() tea.Msg {
		s, _, ok := m.w.Get(cur)
		if !ok || s.Shader() == nil {
			return previewUpdatedMsg{name: cur, ok: false, err: "no sdf"}
		}
		_, rect, err := m.renderer.Render(s, cur, m.proj, m.viewW()*10, m.viewH()*16)
		ok2 := err == nil
		m.w.SetPreview(cur, world.Projection(m.proj), rect.Dx(), rect.Dy(), ok2, errStr(err))
		return previewUpdatedMsg{name: cur, ok: ok2, err: errStr(err)}
	}
}

func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func (m model) submitInputCmd() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	m.input.SetValue("")
	if text == "" {
		return nil
	}

	// Slash commands bypass LLM for reliability and speed.
	if strings.HasPrefix(text, "/") {
		return m.handleSlash(text)
	}

	// Otherwise send through the driver (observable manual tool loop).
	if m.session == nil || m.engine == nil {
		m.status = "LLM engine not ready — using local echo"
		return func() tea.Msg {
			return toolDoneMsg{text: "echo: " + text + " (start with model for real LLM)"}
		}
	}

	// Signal that inferencing has started (for the robot indicator)
	return tea.Batch(
		func() tea.Msg { return inferencingStartMsg{} },
		func() tea.Msg {
			ch := make(chan tea.Msg, 128)

			go func() {
				defer close(ch)

				ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
				defer cancel()

				system := `You are a CAD modeling assistant. Your primary way to build models is by writing and running Lua programs using the low-level "sdf" module.

Use the lua_* tools to create, edit, and execute .lua files. The Lua code supports loops and functions. After lua_run the geometry updates live in the viewport.

Keep responses concise.`

				var driver *bubble.GenerationDriver
				driver = bubble.NewGenerationDriver(bubble.DriverOptions{
					Engine:    m.engine,
					Session:   m.session,
					Tools:     m.tools,
					ThinkMode: m.thinkMode,
					MaxRounds: m.maxRounds,
					ExecuteTools: func(ctx context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
						driver.Emit(bubble.LogEvent{
							Level:   "info",
							Message: fmt.Sprintf("executing %d tool call(s)", len(calls)),
						})
						for _, c := range calls {
							driver.Emit(bubble.LogEvent{
								Level:   "info",
								Message: fmt.Sprintf("tool: %s args=%s", c.Name, truncateForLog(c.Arguments, 200)),
							})
						}

						results, err := m.tools.ExecuteToolCalls(ctx, calls)
						if err != nil {
							driver.Emit(bubble.LogEvent{Level: "error", Message: "tool exec error: " + err.Error()})
							return nil, err
						}

						for _, r := range results {
							driver.Emit(bubble.LogEvent{
								Level:   "info",
								Message: "tool result: " + truncateForLog(r.Content, 200),
							})
						}
						return results, nil
					},
					OnEvent: func(e bubble.Event) {
						select {
						case ch <- driverEventMsg{e: e}:
						default:
						}
					},
				})

				res, err := driver.RunWithPrompt(ctx, system, []ds4.ChatMessage{{Role: "user", Content: text}})
				if err != nil {
					ch <- toolDoneMsg{err: err}
					return
				}

				summary := res.Assistant.Content
				if summary == "" {
					summary = fmt.Sprintf("LLM used %d tool rounds", res.ToolRounds)
				}
				ch <- toolDoneMsg{
					text:      summary,
					reasoning: res.Assistant.ReasoningContent,
				}
			}()

			return generationStartedMsg{ch: ch}
		},
	)
}

func (m model) handleSlash(text string) tea.Cmd {
	parts := strings.Fields(text)
	cmd := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	args := parts[1:]

	return func() tea.Msg {
		var out string
		var err error
		switch cmd {
		case "create":
			if len(args) < 2 {
				err = fmt.Errorf("usage: /create <shape> <name> [k=v ...]")
				break
			}
			shape := args[0]
			name := args[1]
			params := parseKV(args[2:])
			_, err = m.w.Create(name, shape, params)
			out = "created " + name
		case "boolean", "bool":
			if len(args) < 3 {
				err = fmt.Errorf("usage: /boolean <op> <target> <source> [blend=0.1]")
				break
			}
			blend := 0.0
			if len(args) > 3 {
				fmt.Sscanf(args[3], "blend=%f", &blend)
			}
			err = m.w.Boolean(args[0], args[1], args[2], blend)
			out = "boolean ok"
		case "transform", "xform":
			if len(args) < 2 {
				err = fmt.Errorf("usage: /transform <name> <op> [args...]")
				break
			}
			params := parseKV(args[2:])
			err = m.w.Transform(args[0], args[1], params)
			out = "transform ok"
		case "group":
			if len(args) < 2 {
				err = fmt.Errorf("usage: /group <newname> item1 item2 ...")
				break
			}
			err = m.w.Group(args[0], args[1:])
			out = "group ok"
		case "export":
			if len(args) < 1 {
				err = fmt.Errorf("usage: /export <name> <file.stl>")
				break
			}
			name := args[0]
			file := name + ".stl"
			if len(args) > 1 {
				file = args[1]
			}
			// reuse the tool impl
			raw, _ := json.Marshal(map[string]any{"name": name, "filename": file, "resolution_divisions": 256})
			out, err = toolsExportShim(m.w, m.renderer, raw)
		case "save":
			fn := "cadpad-session.cad.json"
			if len(args) > 0 {
				fn = args[0]
			}
			err = m.w.Save(fn)
			out = "saved " + fn
		case "load":
			if len(args) == 0 {
				err = fmt.Errorf("usage: /load file.cad.json")
				break
			}
			err = m.w.Load(args[0])
			out = "loaded"
		case "clear":
			m.w.Clear()
			seedDemo(m.w)
			m.renderer.ClearCache()
			out = "world reset + demo seeded"
		case "help":
			m.showHelp = true
			out = "help shown"
		default:
			err = fmt.Errorf("unknown slash cmd %q — try /help", cmd)
		}
		if err != nil {
			return toolDoneMsg{err: err}
		}
		return toolDoneMsg{text: out}
	}
}

// tiny shim so /export can reuse ExportSTL without exporting the unexported func.
func toolsExportShim(w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	// We know the impl from tools package but to avoid cycle we duplicate 5 lines.
	var a struct {
		Name     string `json:"name"`
		Filename string `json:"filename"`
		ResDiv   int    `json:"resolution_divisions"`
	}
	json.Unmarshal(raw, &a)
	if a.ResDiv == 0 {
		a.ResDiv = 256
	}
	s, _, ok := w.Get(a.Name)
	if !ok {
		return "", fmt.Errorf("not found")
	}
	cfg := simplesdf.STLConfig{ResolutionDivisions: uint(a.ResDiv)}
	if err := s.SaveSTL(a.Filename, cfg); err != nil {
		return "", err
	}
	return "exported " + a.Filename, nil
}

func parseKV(pairs []string) map[string]float64 {
	out := map[string]float64{}
	for _, p := range pairs {
		if k, v, ok := strings.Cut(p, "="); ok {
			var f float64
			fmt.Sscanf(v, "%f", &f)
			out[k] = f
		}
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func spinnerTick() tea.Cmd {
	return bubble.SpinnerTick(bubble.DefaultSpinnerInterval)
}


func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func thinkModeLabel(mode ds4.ThinkMode) string {
	switch mode {
	case ds4.ThinkHigh:
		return "HIGH"
	case ds4.ThinkMax:
		return "MAX"
	default:
		return "OFF"
	}
}

// handleDriverEvent processes live events from the GenerationDriver.
// It is the central place that populates generationLog, luaRuntimeLog,
// tool metrics, and lastActiveLua during inference.
func (m *model) handleDriverEvent(e bubble.Event, cmds *[]tea.Cmd) {
	if e == nil {
		return
	}
	switch ev := e.(type) {
	case bubble.LogEvent:
		entry := fmt.Sprintf("[%s] %s", ev.Level, ev.Message)
		m.generationLog = append(m.generationLog, entry)
		if len(m.generationLog) > 60 {
			m.generationLog = m.generationLog[len(m.generationLog)-60:]
		}

		if strings.Contains(ev.Message, "lua") || strings.Contains(ev.Message, "Lua") ||
			strings.Contains(ev.Message, "Executed") || strings.Contains(ev.Message, "tool result") {
			m.luaRuntimeLog = append(m.luaRuntimeLog, ev.Message)
			if len(m.luaRuntimeLog) > 30 {
				m.luaRuntimeLog = m.luaRuntimeLog[len(m.luaRuntimeLog)-30:]
			}
		}

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

	case bubble.RoundStartedEvent:
		m.generationLog = append(m.generationLog, fmt.Sprintf("── round %d ──", ev.Round+1))
		if len(m.generationLog) > 60 {
			m.generationLog = m.generationLog[len(m.generationLog)-60:]
		}
		m.status = fmt.Sprintf("generating (round %d)", ev.Round+1)

	case bubble.ErrorEvent:
		if ev.Err != nil {
			m.lastErr = ev.Err.Error()
			m.generationLog = append(m.generationLog, "[ERROR] "+ev.Err.Error())
		}
	}
}

// extractLuaPath pulls a .lua path out of the rich LogEvents we emit during
// driver tool execution. Used to know which file to snapshot for timestamped exports.
// truncateForLog keeps LogEvent payloads readable.
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func extractLuaPath(msg string) string {
	if strings.Contains(msg, `"path":`) {
		re := regexp.MustCompile(`"path"\s*:\s*"([^"]+\.lua)"`)
		if m := re.FindStringSubmatch(msg); len(m) == 2 {
			return m[1]
		}
	}
	re := regexp.MustCompile(`(?:Wrote|Executed|Appended to|Replaced[^ ]* in)\s+([^\s"]+\.lua)`)
	if m := re.FindStringSubmatch(msg); len(m) == 2 {
		return m[1]
	}
	re2 := regexp.MustCompile(`([A-Za-z0-9_./-]+\.lua)`)
	for _, p := range re2.FindAllString(msg, -1) {
		if strings.HasSuffix(p, ".lua") && !strings.Contains(p, " ") {
			return p
		}
	}
	return ""
}

// saveTimestampedLua writes a copy of the last active Lua file (if any) with a
// time-dated name into the workspace. This is the cadpad equivalent of svgpad
// writing svgpad.20060102_150405.svg on successful generation.
func (m *model) saveTimestampedLua() string {
	if m.lastActiveLua == "" || m.luaWorkspace == "" {
		return ""
	}
	data, err := os.ReadFile(m.lastActiveLua)
	if err != nil {
		return ""
	}
	ts := time.Now().Format("20060102_150405")
	fname := fmt.Sprintf("cadpad.%s.lua", ts)
	dst := filepath.Join(m.luaWorkspace, fname)
	if err := os.WriteFile(dst, data, 0644); err != nil {
		m.logger.Printf("timestamped lua save failed: %v", err)
		return ""
	}
	m.logger.Printf("[SAVE] %s (%d bytes)", fname, len(data))
	return fname
}

// ── libds4 log overlay (ctrl+n) ───────────────────────────────────────────────
// These methods + logOverlay are intentionally similar to the ones in
// glyphpad and svgpad so users have a consistent experience across the
// ds4go-apps TUIs. The overlay is useful even with --no-engine because the
// logBuf is installed unconditionally in main.go.

func (m model) logPageSize() int {
	h := m.height - 6
	if h < 1 {
		return 1
	}
	return h
}

// logScrollBy moves the log view by delta lines. -1 means "follow tail".
func (m model) logScrollBy(delta int) int {
	total, innerH := m.logVisibleMetrics()
	maxTop := total - innerH
	if maxTop < 0 {
		maxTop = 0
	}
	top := m.logTop
	if top < 0 { // was following tail — anchor first
		top = maxTop
	}
	top += delta
	if top < 0 {
		top = 0
	}
	if top >= maxTop {
		return -1 // back at bottom → resume following
	}
	return top
}

func (m model) logVisibleMetrics() (total, innerH int) {
	innerW := m.width - 6
	if innerW < 10 {
		innerW = 10
	}
	innerH = m.height - 6
	if innerH < 1 {
		innerH = 1
	}
	lines := m.logBuf.Lines()
	if len(lines) == 0 {
		return 0, innerH
	}
	wrapped := lipgloss.NewStyle().Width(innerW).Render(strings.Join(lines, "\n"))
	return strings.Count(wrapped, "\n") + 1, innerH
}

// titledBox draws a title into the top border of a box (subtle polish).
func titledBox(rendered, title string) string {
	if title != "" {
		title = " " + title + " "
	}
	lines := strings.SplitN(rendered, "\n", 2)
	if len(lines) == 0 {
		return rendered
	}
	top := []rune(lines[0])
	label := []rune(title)
	if len(top) < len(label)+4 {
		return rendered
	}
	newTop := string(top[:2]) + titleStyle.Render(string(label)) + string(top[2+len(label):])
	if len(lines) == 2 {
		return newTop + "\n" + lines[1]
	}
	return newTop
}

// logOverlay renders the scrollable libds4 diagnostic overlay (ctrl+n).
// It is intentionally useful in --no-engine mode.
func (m model) logOverlay() string {
	// Engine (libds4) logs — the traditional ctrl+n content.
	engineLines := m.logBuf.Lines()
	if len(engineLines) == 0 {
		engineLines = []string{dimStyle.Render("(no libds4 diagnostics yet)")}
	}

	// High-level model reasoning that we routed here (see toolDoneMsg handling).
	var reasoningSection []string
	if len(m.reasoningLog) > 0 {
		reasoningSection = append(reasoningSection, "", dimStyle.Render("── Model Reasoning (routed from LLM) ──"))
		reasoningSection = append(reasoningSection, m.reasoningLog...)
	}

	// Combine for display in one scrollable log box.
	allContent := append([]string(nil), engineLines...)
	allContent = append(allContent, reasoningSection...)

	if len(allContent) == 0 {
		allContent = []string{dimStyle.Render("(no logs yet)")}
	}

	innerW := m.width - 6
	if innerW < 10 {
		innerW = 10
	}
	innerH := m.height - 6
	if innerH < 1 {
		innerH = 1
	}

	wrapped := lipgloss.NewStyle().Width(innerW).Render(strings.Join(allContent, "\n"))
	allLines := strings.Split(wrapped, "\n")

	total := len(allLines)
	maxTop := total - innerH
	if maxTop < 0 {
		maxTop = 0
	}

	var start int
	following := m.logTop < 0
	if following {
		start = maxTop
	} else {
		start = m.logTop
		if start > maxTop {
			start = maxTop
		}
	}
	end := start + innerH
	if end > total {
		end = total
	}

	body := strings.Join(allLines[start:end], "\n")

	mode := "TAIL"
	if !following {
		mode = "FROZEN"
	}
	hint := fmt.Sprintf("  ↑/↓/j/k · pgup/pgdn  ·  esc close   [%s %d/%d]   (engine + thinking)", mode, start, maxTop)
	body += "\n\n" + dimStyle.Render(hint)

	title := "cadpad · logs (engine + thinking)"
	if m.thinkMode != ds4.ThinkNone {
		title += " [reason:" + thinkModeLabel(m.thinkMode) + "]"
	}

	box := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Render(body), title)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// ── unified prompt window keymap (editmode package) ──────────────────────────

func (m model) keymap() editmode.Keymap {
	def := editmode.DefaultEditBindings()

	// Edit mode (box focused): standard editor keys + our submit/esc.
	edit := make([]editmode.Binding, 0, len(def)+2)
	edit = append(edit, editmode.Binding{Keys: "ctrl+n", Desc: "log"})
	edit = append(edit, def...)

	// Command mode (box not focused): navigation + actions + 'e' to edit prompt.
	cmd := []editmode.Binding{
		{"e", "edit prompt"},
		{"j/k", "nav objects"},
		{"1/2/3/tab", "proj"},
		{"p", "preview"},
		{"s", "save (json + timestamped .lua)"},
		{"r", "refresh"},
		{"t", "thinking"},
		{"ctrl+n", "log"},
		{"?", "help"},
		{"ctrl+c", "quit"},
	}

	return editmode.Keymap{Edit: edit, Command: cmd}
}

