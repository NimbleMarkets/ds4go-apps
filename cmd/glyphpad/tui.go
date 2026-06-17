package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/NimbleMarkets/ds4go-apps/internal/editmode"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/headerbar"
	"github.com/NimbleMarkets/ntcharts/v2/canvas"
	"github.com/charmbracelet/x/ansi"
)

const (
	headerH         = 1
	footerH         = 1
	inputH          = 3  // 1 content line + 2 border chars
	defaultThinkBox = 12 // initial thinking-box content height
	minThinkBox     = 1  // smallest thinking-box content height
	minRightW       = 20 // right column keeps at least this width; canvas clips first
	commentBoxH     = 8  // Comment box total height; the Code box takes the rest
)

// ── message types ────────────────────────────────────────────────────────────

type chatMsg struct {
	role    string
	content string
}

type submitMsg struct{ text string }
type stepTickMsg struct{}

// engineReadyMsg is delivered from the goroutine that opens the ds4 engine
// and session. The model holds nil engine/session until this lands.
type engineReadyMsg engineinit.Result

// ── model ────────────────────────────────────────────────────────────────────

type model struct {
	width, height    int
	canvasW, canvasH int // locked canvas content size — the LLM's coordinate space

	engine       *ds4.Engine // nil until engineReadyMsg
	session      *ds4.Session
	lib          *ds4.Library      // resolved in main, used by Init's goroutine
	engOpts      ds4.EngineOptions // captured to fire async open
	engineStatus engineinit.Status // drives the header badge
	engineErr    error             // set when StatusError
	modelPath    string
	mtpPath      string
	hasMTP       bool
	mtpDraft     int
	backend      string
	workDir      string
	showInfo     bool           // info overlay is up (m in command mode)
	showLog      bool           // libds4 log overlay is up (ctrl+n)
	logBuf       *ds4log.Buffer // captured libds4 diagnostics
	logTop       int            // absolute first-visible line; -1 = follow tail

	history    []chatMsg
	rawBuf     []byte             // raw LLM response for the current turn
	gen        *bubble.Generation // in-flight generation; nil when idle
	generating bool
	statusText string
	errText    string
	lastErr    error

	parser    *Parser
	canvas    canvas.Model
	descText  string
	thinkText string

	showThinking bool
	thinkBoxH    int // thinking-box content height (shift+up/down adjusts)
	thinkMode    ds4.ThinkMode

	stepN   int  // -1 = live (canvas shows all commands); >=0 = show commands [0:stepN]
	running bool // a stepped "run" animation is in progress

	input textinput.Model

	// metrics
	genStart       time.Time
	firstTokenTime time.Time
	genEnd         time.Time
	tokenCount     int
	ctxPos         int
	ctxSize        int

	logger *log.Logger
	debug  bool
}

func newModel(app *appinit.App) model {
	// Local aliases so the body below is unchanged from the pre-appinit form.
	lib := app.Lib
	engOpts := app.EngineOpts
	ctxSize := app.Flags.Ctx
	modelPath := app.EngineOpts.ModelPath
	mtpPath := app.EngineOpts.MTPPath
	backend := app.Flags.Backend
	logger := app.Logger
	logBuf := app.LogBuf
	debug := app.Flags.Debug
	ti := textinput.New()
	ti.Placeholder = "Ask anything..."
	ti.Focus()

	wd, _ := os.Getwd()
	return model{
		lib:          lib,
		engOpts:      engOpts,
		engineStatus: engineinit.StatusInit,
		modelPath:    modelPath,
		mtpPath:      mtpPath,
		backend:      backend,
		workDir:      wd,
		parser:       NewParser(),
		canvas:       canvas.New(40, 20),
		input:        ti,
		statusText:   "GPU initializing…",
		showThinking: true,
		thinkBoxH:    defaultThinkBox,
		thinkMode:    ds4.ThinkNone,
		stepN:        -1,
		ctxSize:      ctxSize,
		logger:       logger,
		logBuf:       logBuf,
		logTop:       -1, // follow the tail by default
		debug:        debug,
	}
}

// ── BubbleTea interface ───────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	// Open the engine and session in the goroutine bubbletea spawns for
	// this Cmd. Until engineReadyMsg lands the TUI is fully interactive
	// but submissions are gated; the badge shows the init/ready state.
	lib, opts, ctxSize := m.lib, m.engOpts, m.ctxSize
	return func() tea.Msg {
		return engineReadyMsg(engineinit.Open(lib, opts, ctxSize))
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resize()
		if m.canvasW == 0 { // first window size — lock the canvas to it
			m = m.lockCanvas()
		}

	case tea.KeyPressMsg:
		// Log overlay has its own scrollable input handling — only esc
		// (or quit) leaves it; the rest of the key set scrolls. Info
		// stays "any-key-dismisses" since it's not scrollable.
		if m.showLog {
			switch msg.String() {
			case "ctrl+c", "ctrl+q":
				return m, tea.Quit
			case "esc":
				m.showLog = false
				m.logTop = -1 // reset to follow next time
			case "up":
				m.logTop = m.logScrollBy(-1)
			case "down":
				m.logTop = m.logScrollBy(1)
			case "pgup":
				m.logTop = m.logScrollBy(-m.logPageSize())
			case "pgdown":
				m.logTop = m.logScrollBy(m.logPageSize())
			}
			return m, nil
		}
		if m.showInfo {
			if s := msg.String(); s == "ctrl+c" || s == "ctrl+q" {
				return m, tea.Quit
			}
			m.showInfo = false
			return m, nil
		}

		// Keys that work in BOTH modes (edit and command).
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit

		case "ctrl+n": // libds4 log overlay
			m.showLog = true
			return m, nil

		case "esc":
			// Generating → abort. Else if targeted → escape to command
			// mode. In command mode esc is a no-op.
			if m.generating && m.gen != nil {
				m.gen.Cancel()
				m.statusText = "Aborting..."
				return m, nil
			}
			if m.input.Focused() {
				m.input.Blur()
			}
			return m, nil

		case "enter":
			if m.input.Focused() && !m.generating {
				if m.engineStatus != engineinit.StatusReady {
					m.statusText = "GPU initializing… please wait"
					return m, nil
				}
				text := strings.TrimSpace(m.input.Value())
				if text != "" {
					m.input.Blur() // lock the sent prompt in place
					cmds = append(cmds, func() tea.Msg { return submitMsg{text} })
				}
			}
			return m, tea.Batch(cmds...)

		case "shift+up":
			if m.showThinking {
				m.thinkBoxH++
				m = m.resize()
			}
			return m, nil

		case "shift+down":
			if m.showThinking {
				m.thinkBoxH--
				m = m.resize()
			}
			return m, nil
		}

		// In edit mode, every other key types into the box.
		if m.input.Focused() {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

		// Command mode: bare-letter commands.
		switch msg.String() {
		case "e": // edit (target the box)
			if !m.generating {
				cmds = append(cmds, m.input.Focus())
			}

		case "n": // new prompt — clear and target
			m.input.SetValue("")
			m.history = nil
			m.lastErr = nil
			m.errText = ""
			m.descText = ""
			m.thinkText = ""
			m.parser.Reset()
			m.canvas.Clear()
			cmds = append(cmds, m.input.Focus())

		case "c": // continue generating
			if m.generating {
				return m, nil
			}
			isTruncated := false
			if m.lastErr != nil {
				if errors.Is(m.lastErr, ds4.ErrContextFull) || m.lastErr.Error() == "ds4go: session context full" || errors.Is(m.lastErr, context.Canceled) {
					isTruncated = true
				}
			}
			if isTruncated {
				m.generating = true
				m.statusText = "Continuing..."
				m.errText = ""
				m.genStart = time.Now()
				m.firstTokenTime = time.Time{}
				m.genEnd = time.Time{}
				m.tokenCount = 0
				var waitCmd tea.Cmd
				m.gen, waitCmd = bubble.Start(m.generateContinue)
				cmds = append(cmds, waitCmd)
			}
			return m, tea.Batch(cmds...)

		case "m": // info overlay
			m.showInfo = true

		case "t":
			m.showThinking = !m.showThinking
			m = m.resize()

		case "r":
			// Cycle reasoning: OFF → HIGH → MAX → OFF.
			switch m.thinkMode {
			case ds4.ThinkNone:
				m.thinkMode = ds4.ThinkHigh
			case ds4.ThinkHigh:
				m.thinkMode = ds4.ThinkMax
			default:
				m.thinkMode = ds4.ThinkNone
			}
			m.logger.Printf("[REASONING] %s", m.thinkModeLabel())

		case "f": // step forward one command
			m.running = false
			total := len(m.parser.CanvasLines())
			if m.stepN >= 0 && m.stepN < total {
				m.stepN++
				if m.stepN >= total {
					m.stepN = -1
				}
				m, _ = m.redrawCanvas()
			}

		case "b": // step back one command
			m.running = false
			if total := len(m.parser.CanvasLines()); total > 0 {
				if m.stepN < 0 {
					m.stepN = total
				}
				if m.stepN > 0 {
					m.stepN--
				}
				m, _ = m.redrawCanvas()
			}

		case "g": // run: animate the command list
			if total := len(m.parser.CanvasLines()); total > 0 {
				if m.running {
					m.running = false
				} else {
					if m.stepN < 0 {
						m.stepN = 0
					}
					m.running = true
					m, _ = m.redrawCanvas()
					cmds = append(cmds, stepTick())
				}
			}

		case "l": // re-fit the canvas to the current window size
			m = m.lockCanvas()

		case "<", ",":
			if m.engine != nil {
				cur := m.engine.Power()
				newPower := cur - 10
				if newPower < 1 {
					newPower = 1
				}
				if err := m.engine.SetPower(newPower); err == nil {
					m.statusText = fmt.Sprintf("GPU Power set to %d%%", newPower)
				} else {
					m.statusText = fmt.Sprintf("Error setting power: %v", err)
				}
			} else {
				p := m.engOpts.PowerPercent
				if p == 0 {
					p = 100
				}
				p -= 10
				if p < 1 {
					p = 1
				}
				m.engOpts.PowerPercent = p
				m.statusText = fmt.Sprintf("Initial GPU Power set to %d%%", p)
			}

		case ">", ".":
			if m.engine != nil {
				cur := m.engine.Power()
				newPower := cur + 10
				if newPower > 100 {
					newPower = 100
				}
				if err := m.engine.SetPower(newPower); err == nil {
					m.statusText = fmt.Sprintf("GPU Power set to %d%%", newPower)
				} else {
					m.statusText = fmt.Sprintf("Error setting power: %v", err)
				}
			} else {
				p := m.engOpts.PowerPercent
				if p == 0 {
					p = 100
				}
				p += 10
				if p > 100 {
					p = 100
				}
				m.engOpts.PowerPercent = p
				m.statusText = fmt.Sprintf("Initial GPU Power set to %d%%", p)
			}

		case "k": // clear the canvas, output, and conversation
			if !m.generating {
				m.parser.Reset()
				m.canvas.Clear()
				m.rawBuf = m.rawBuf[:0]
				m.descText = ""
				m.thinkText = ""
				m.history = nil
				m.stepN = -1
				m.running = false
				m.errText = ""
				m.statusText = "Ready"
				m.ctxPos = 0
				m.tokenCount = 0
				m.genStart = time.Time{}
				m.firstTokenTime = time.Time{}
				m.genEnd = time.Time{}
				m.logger.Printf("[CLEAR]")
			}
		}

	case engineReadyMsg:
		if msg.Err != nil {
			m.engineStatus = engineinit.StatusError
			m.engineErr = msg.Err
			m.statusText = "Error"
			m.errText = msg.Err.Error()
			m.logger.Printf("[ENGINE] open failed: %v", msg.Err)
			return m, nil
		}
		m.engine = msg.Engine
		m.session = msg.Session
		m.hasMTP = msg.HasMTP
		m.mtpDraft = msg.MTPDraft
		m.ctxSize = msg.Session.Ctx()
		m.engineStatus = engineinit.StatusReady
		m.statusText = "Ready"
		m.logger.Printf("[ENGINE] ready  mtp=%v mtpDraft=%d ctx=%d",
			m.hasMTP, m.mtpDraft, m.ctxSize)
		return m, nil

	case submitMsg:
		m.logger.Printf("[USER] %s", msg.text)
		m.history = append(m.history, chatMsg{"user", msg.text})
		m.parser.Reset()
		m.rawBuf = m.rawBuf[:0]
		m.descText = ""
		m.thinkText = ""
		m.canvas.Clear()
		m.generating = true
		m.statusText = "Generating..."
		m.errText = ""
		m.genStart = time.Now()
		m.firstTokenTime = time.Time{}
		m.genEnd = time.Time{}
		m.tokenCount = 0
		m.stepN = -1
		m.running = false
		var waitCmd tea.Cmd
		m.gen, waitCmd = bubble.Start(m.generate)
		cmds = append(cmds, waitCmd)

	case bubble.TokenMsg:
		text := string(msg)
		if m.debug {
			m.logger.Printf("[TOKEN] %s", strconv.Quote(text))
		}
		if m.firstTokenTime.IsZero() {
			m.firstTokenTime = time.Now()
		}
		m.tokenCount++
		m.rawBuf = append(m.rawBuf, text...)
		m.parser.Feed(text)
		m, _ = m.redrawCanvas()
		m.descText = m.parser.DescText()
		m.thinkText = m.parser.ThinkText()
		cmds = append(cmds, m.gen.Wait())

	case bubble.DoneMsg:
		m.generating = false
		m.lastErr = msg.Err
		if m.gen.Canceled() {
			m.statusText = "Aborted"
		} else {
			m.statusText = "Ready"
		}
		m.gen.Cancel() // release the context's resources
		m.genEnd = time.Now()
		m.ctxPos = msg.CtxPos
		if msg.Err != nil {
			if errors.Is(msg.Err, ds4.ErrContextFull) || msg.Err.Error() == "ds4go: session context full" {
				m.statusText = "Ready · Context full"
				m.errText = "Session context capacity reached. Press 'c' to continue or 'n' for a new prompt."
			} else {
				m.errText = msg.Err.Error()
				if m.statusText != "Aborted" {
					m.statusText = "Error"
				}
			}
			m.logger.Printf("[ERROR] %v", msg.Err)
		} else {
			// Store a clean assistant turn (canvas + description only). Never
			// keep the raw <think> block — replaying it poisons later turns.
			clean, _ := json.Marshal(struct {
				Canvas      string `json:"canvas"`
				Description string `json:"description"`
			}{string(m.parser.Canvas), m.parser.DescText()})
			m.history = append(m.history, chatMsg{"assistant", string(clean)})
			var applied int
			m, applied = m.redrawCanvas()
			switch {
			case len(m.parser.Canvas) == 0:
				m.statusText = "Ready · empty canvas — model emitted no JSON"
			case applied == 0:
				m.statusText = "Ready · 0 commands drew — coordinates off-canvas?"
			}
			m.logger.Printf("[DONE] %d tok  %.1f tok/s  ttft=%s  gen=%s  %d bytes  think=%d  canvas=%d cmds (%d applied)  desc=%d bytes  ctx=%d/%d",
				m.tokenCount, m.decodeSpeed(), fmtDuration(m.ttft()), fmtDuration(m.genTime()),
				len(m.rawBuf), len(m.parser.Think), len(m.parser.CanvasLines()), applied, len(m.parser.Desc),
				m.ctxPos, m.ctxSize)
			m.logger.Printf("[CANVAS] %s", strconv.Quote(string(m.parser.Canvas)))
		}
		m.gen = nil

	case stepTickMsg:
		if m.running {
			total := len(m.parser.CanvasLines())
			m.stepN++
			if m.stepN >= total {
				m.stepN = -1
				m.running = false
			}
			m, _ = m.redrawCanvas()
			if m.running {
				cmds = append(cmds, stepTick())
			}
		}

	default:
		// Forward other messages (cursor blink etc.) to the text input.
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func stepTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return stepTickMsg{} })
}

// redrawCanvas repaints the canvas from the parsed draw commands, honoring the
// step cursor: stepN < 0 draws every command, stepN >= 0 draws only [0:stepN].
// Returns the model and the number of commands that drew on-canvas.
func (m model) redrawCanvas() (model, int) {
	lines := m.parser.CanvasLines()
	if m.stepN >= 0 && m.stepN < len(lines) {
		lines = lines[:m.stepN]
	}
	m.canvas.Clear()
	return m, drawCommands(&m.canvas, lines)
}

func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// ── layout ───────────────────────────────────────────────────────────────────

// thinkModeLabel is the fixed-width (4-char) footer label for the reasoning mode.
func (m model) thinkModeLabel() string {
	switch m.thinkMode {
	case ds4.ThinkNone:
		return "OFF "
	case ds4.ThinkMax:
		return "MAX "
	default:
		return "HIGH"
	}
}

// thinkingHeight is the total rows the thinking box occupies (0 when hidden).
func (m model) thinkingHeight() int {
	if m.showThinking {
		return m.thinkBoxH + 2 // + border
	}
	return 0
}

// middleHeight is the total rows for the canvas/description row.
func (m model) middleHeight() int {
	h := m.height - headerH - footerH - inputH - m.thinkingHeight()
	if m.errText != "" {
		h -= 1
	}
	if h < 3 {
		h = 3
	}
	return h
}

// resize reflows window-dependent layout. It does NOT touch the canvas — the
// canvas keeps its locked size across window resizes (see lockCanvas).
func (m model) resize() model {
	if m.width == 0 || m.height == 0 {
		return m
	}
	// Clamp the thinking box so the canvas/description row keeps >=5 rows.
	maxThink := m.height - 12
	if maxThink < minThinkBox {
		maxThink = minThinkBox
	}
	if m.thinkBoxH < minThinkBox {
		m.thinkBoxH = minThinkBox
	} else if m.thinkBoxH > maxThink {
		m.thinkBoxH = maxThink
	}
	m.input.SetWidth(m.width - 4) // box content width minus the "> " prompt
	return m.applyCanvasView()
}

// applyCanvasView sizes the canvas's visible viewport (ViewWidth/ViewHeight) to
// the space the window currently affords. The content grid — the LLM's locked
// coordinate space — is left alone; the view just clips to a top-left window
// when the window is smaller than the locked canvas.
func (m model) applyCanvasView() model {
	viewW := m.canvasW
	if maxW := m.width - minRightW - 2; viewW > maxW {
		viewW = maxW
	}
	if viewW < 1 {
		viewW = 1
	}
	viewH := m.canvasH
	if maxH := m.middleHeight() - 2; viewH > maxH {
		viewH = maxH
	}
	if viewH < 1 {
		viewH = 1
	}
	m.canvas.ViewWidth = viewW
	m.canvas.ViewHeight = viewH
	return m
}

// fitCanvasSize is the canvas content size that fits the current window.
func (m model) fitCanvasSize() (w, h int) {
	w = m.width/2 - 2
	h = m.middleHeight() - 2
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

// lockCanvas fixes the canvas to the size that fits the current window. The
// canvas keeps this size until the next lockCanvas (ctrl+l); plain window
// resizes leave it alone so the LLM's coordinate space stays stable.
func (m model) lockCanvas() model {
	if m.width == 0 || m.height == 0 {
		return m
	}
	m.canvasW, m.canvasH = m.fitCanvasSize()
	m.canvas.Resize(m.canvasW, m.canvasH)
	m, _ = m.redrawCanvas()
	return m.applyCanvasView()
}

func (m model) render() string {
	if m.width == 0 {
		return "Initializing..."
	}
	if m.showInfo {
		return m.infoOverlay()
	}
	if m.showLog {
		return m.logOverlay()
	}

	viewW := m.canvas.ViewWidth
	rightW := m.width - (viewW + 2)
	if rightW < 4 {
		rightW = 4
	}
	middleH := m.middleHeight()

	// Header bar
	modelName := filepath.Base(m.modelPath)
	status := m.statusText
	if m.errText != "" {
		cleanErr := strings.ReplaceAll(m.errText, "\n", " | ")
		status = "Error: " + cleanErr
		if len(status) > 40 {
			status = status[:37] + "..."
		}
	}
	canvasInfo := fmt.Sprintf("canvas %d×%d", m.canvasW, m.canvasH)
	if fw, fh := m.fitCanvasSize(); fw != m.canvasW || fh != m.canvasH {
		canvasInfo += " (l)" // window changed — l (command mode) would re-fit
	}
	metricsText := m.metricsText()
	if m.hasMTP {
		if m.mtpDraft > 1 {
			metricsText += fmt.Sprintf(" · MTP · %d", m.mtpDraft)
		} else {
			metricsText += " · MTP (off)"
		}
	}
	headerContent := headerbar.Layout(
		m.width,
		fmt.Sprintf(" ds4go-glyphpad │ %s │ %s │ ", modelName, canvasInfo),
		status,
		strings.TrimLeft(metricsText, " ·"),
		engineinit.Badge(m.engineStatus),
	)
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#ffffff")).
		Background(lipgloss.Color("#1d3557")).
		Render(headerContent)

	// Canvas panel — always the full middle height so it matches the right
	// column. The locked canvas view sits at the top; if it is shorter than
	// the box (window grew since the last lock) the rest pads blank. ctrl+l
	// re-fits the canvas to reclaim that space.
	canvasPanel := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(viewW+2).
		Height(middleH).
		MaxHeight(middleH).
		Render(m.canvas.View()), "Drawing")

	// Right column: small fixed Comment box on top; the Code box grows to fill
	// the rest of the column, down to the thinking box.
	descBoxH := commentBoxH
	if descBoxH > middleH-3 {
		descBoxH = middleH - 3
	}
	if descBoxH < 3 {
		descBoxH = 3
	}
	codeBoxH := middleH - descBoxH
	if codeBoxH < 3 {
		codeBoxH = 3
	}

	desc := m.descText
	if desc == "" && m.generating {
		desc = "..."
	}
	descPanel := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(rightW).
		Height(descBoxH).
		MaxHeight(descBoxH).
		Render(tailLines(wrapText(desc, rightW-2), descBoxH-2)), "Comment")

	code := m.codeBoxText(codeBoxH - 2)
	codePanel := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(rightW).
		Height(codeBoxH).
		MaxHeight(codeBoxH).
		Render(code), "Code")

	rightCol := lipgloss.JoinVertical(lipgloss.Left, descPanel, codePanel)
	middle := lipgloss.JoinHorizontal(lipgloss.Top, canvasPanel, rightCol)

	sections := []string{header}
	if m.errText != "" {
		errStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("203")).
			Foreground(lipgloss.Color("255")).
			Bold(true).
			Width(m.width)
		msg := " ERROR: " + strings.ReplaceAll(m.errText, "\n", " | ")
		if lipgloss.Width(msg) > m.width {
			msg = ansi.Truncate(msg, m.width-3, "...")
		}
		if w := lipgloss.Width(msg); w < m.width {
			msg += strings.Repeat(" ", m.width-w)
		}
		sections = append(sections, errStyle.Render(msg))
	}
	sections = append(sections, middle)

	// Thinking box (full width, toggleable with ctrl+t)
	if m.showThinking {
		think := tailLines(wrapText(m.thinkText, m.width-2), m.thinkBoxH)
		if think == "" {
			switch {
			case m.generating:
				think = "..."
			case m.thinkMode == ds4.ThinkNone:
				think = "(reasoning off — r to enable in command mode)"
			}
		}
		thinkingPanel := titledBox(lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Width(m.width).
			Height(m.thinkBoxH+2).
			MaxHeight(m.thinkBoxH+2).
			Render(think), "Thinking")
		sections = append(sections, thinkingPanel)
	}

	// Input panel
	promptTitle := "Prompt"
	if !m.input.Focused() {
		promptTitle = "Sent — e edit · n new"
	}
	inputPanel := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(m.width).
		Height(inputH).
		MaxHeight(inputH).
		Render(m.input.View()), promptTitle)
	sections = append(sections, inputPanel)

	// Footer help bar — swaps with mode (edit vs command). The step-mode
	// animation uses a constrained command list built inside m.keymap().
	help := " " + m.keymap().FooterText(m.input.Focused())
	footer := lipgloss.NewStyle().
		Width(m.width).
		MaxHeight(1).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("8")).
		Render(help)
	sections = append(sections, footer)

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// ── metrics ──────────────────────────────────────────────────────────────────

// ttft is the time from generation request to the first streamed token.
func (m model) ttft() time.Duration {
	if m.firstTokenTime.IsZero() || m.genStart.IsZero() {
		return 0
	}
	return m.firstTokenTime.Sub(m.genStart)
}

// genTime is the decode duration from first token to last token.
func (m model) genTime() time.Duration {
	if m.firstTokenTime.IsZero() {
		return 0
	}
	end := time.Now()
	if !m.genEnd.IsZero() {
		end = m.genEnd
	}
	return end.Sub(m.firstTokenTime)
}

// decodeSpeed is tokens/sec measured over the decode phase (first token → now,
// or → genEnd once finished).
func (m model) decodeSpeed() float64 {
	if m.firstTokenTime.IsZero() {
		return 0
	}
	end := time.Now()
	if !m.generating && !m.genEnd.IsZero() {
		end = m.genEnd
	}
	secs := end.Sub(m.firstTokenTime).Seconds()
	if secs <= 0 {
		return 0
	}
	return float64(m.tokenCount) / secs
}

// metricsText is the right-aligned header readout (empty before the first turn).
func (m model) metricsText() string {
	if m.genStart.IsZero() {
		return ""
	}
	ctxPct := 0
	if m.ctxSize > 0 {
		ctxPct = m.ctxPos * 100 / m.ctxSize
	}
	return fmt.Sprintf("%d tok · %.1f tok/s · ttft %s · gen %s · ctx %d%%",
		m.tokenCount, m.decodeSpeed(), fmtDuration(m.ttft()), fmtDuration(m.genTime()), ctxPct)
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// wrapText wraps s to the given display width, breaking on spaces.
func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		lineW := lipgloss.Width(line)
		for _, word := range words[1:] {
			wW := lipgloss.Width(word)
			if lineW+1+wW <= width {
				line += " " + word
				lineW += 1 + wW
			} else {
				out = append(out, line)
				line, lineW = word, wW
			}
		}
		out = append(out, line)
	}
	return out
}

// tailLines joins the last n lines with newlines.
func tailLines(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// sanitizeLines strips display-breaking control characters from each line so
// the raw model output can't corrupt the TUI (e.g. injected ANSI escapes).
func sanitizeLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = sanitizeLine(l)
	}
	return out
}

func sanitizeLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// drop control characters (incl. ESC) — never render them
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Debugger syntax-coloring styles.
var (
	codeOpStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)    // command name
	codeNumStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))              // coordinates / inert lines
	codeStepStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))              // · executed marker
	codeCurStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)   // ▸ current marker
	codeCommentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("108")).Italic(true) // ; comment
)

// codeBoxText renders every emitted draw-command line, sanitized and syntax-
// colored, as a window of `height` lines. While stepping, executed lines are
// marked "· ", the next command "▸ ", and the window follows the step cursor.
func (m model) codeBoxText(height int) string {
	lines := sanitizeLines(m.parser.CanvasLines())
	if len(lines) == 0 {
		if m.generating {
			return "..."
		}
		return ""
	}
	pen := "" // pen color carried down the command list, like drawCommands
	disp := make([]string, len(lines))
	for i, l := range lines {
		mark := "  "
		if m.stepN >= 0 {
			switch {
			case i < m.stepN:
				mark = codeStepStyle.Render("· ")
			case i == m.stepN:
				mark = codeCurStyle.Render("▸ ")
			}
		}
		var colored string
		colored, pen = colorizeCommand(l, pen)
		disp[i] = mark + colored
	}
	focus := len(lines) - 1
	if m.stepN >= 0 && m.stepN < len(lines) {
		focus = m.stepN
	}
	return windowLines(disp, focus, height)
}

// colorizeCommand renders one debugger line: the command syntax-colored, with
// any assembly-style "; comment" tail dimmed. Returns the styled line and the
// pen color in effect after it.
func colorizeCommand(line, pen string) (string, string) {
	code, comment := line, ""
	if i := strings.IndexByte(line, ';'); i >= 0 {
		code, comment = strings.TrimRight(line[:i], " \t"), line[i:]
	}
	rendered, newPen := colorizeCode(code, pen)
	if comment != "" {
		styled := codeCommentStyle.Render(comment)
		if rendered == "" {
			rendered = styled
		} else {
			rendered += " " + styled
		}
	}
	return rendered, newPen
}

// colorizeCode syntax-colors one draw-command line (comment already removed):
// command name in an accent, coordinates dim, glyph/text in the active pen
// color. Unknown ops, malformed coords, and still-streaming partial lines
// render dim.
func colorizeCode(line, pen string) (string, string) {
	op, rest := cutField(line)
	if op == "" {
		return "", pen
	}
	if strings.ToLower(op) == "color" {
		name, _ := cutField(rest)
		clean := strings.ToLower(strings.Trim(name, `"`))
		switch clean {
		case "default", "reset", "none":
			return codeOpStyle.Render(op) + " " + codeNumStyle.Render(name), ""
		}
		if col, ok := parseColor(clean); ok {
			swatch := lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Render(name)
			return codeOpStyle.Render(op) + " " + swatch, col
		}
		return codeNumStyle.Render(line), pen // unknown color name
	}

	nNums, hasGlyph := commandArity(strings.ToLower(op))
	if nNums < 0 {
		return codeNumStyle.Render(line), pen // unknown op
	}
	nums, after, ok := cutInts(rest, nNums)
	if !ok {
		return codeNumStyle.Render(line), pen // malformed or still streaming
	}
	parts := make([]string, 0, nNums+2)
	parts = append(parts, codeOpStyle.Render(op))
	for _, n := range nums {
		parts = append(parts, codeNumStyle.Render(strconv.Itoa(n)))
	}
	if hasGlyph {
		if g := strings.Trim(after, `"`); g != "" {
			gs := lipgloss.NewStyle()
			if pen != "" {
				gs = gs.Foreground(lipgloss.Color(pen))
			}
			parts = append(parts, gs.Render(g))
		}
	}
	return strings.Join(parts, " "), pen
}

var (
	titleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	infoHeadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	infoDimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

// keymap builds the swapping footer bindings for the current state. The
// Edit list is DefaultEditBindings() with {ctrl+n log} spliced in before
// the trailing quit, so the log overlay key is discoverable while typing.
// The Command list is the full command set; during step-mode animation a
// constrained list is shown so the help reflects what actually responds.
func (m model) keymap() editmode.Keymap {
	def := editmode.DefaultEditBindings()
	edit := make([]editmode.Binding, 0, len(def)+1)
	edit = append(edit, def[:len(def)-1]...)
	edit = append(edit, editmode.Binding{Keys: "ctrl+n", Desc: "log"})
	edit = append(edit, def[len(def)-1])

	var cmd []editmode.Binding
	if m.stepN >= 0 {
		cmd = []editmode.Binding{
			{Keys: fmt.Sprintf("STEP %d/%d", m.stepN, len(m.parser.CanvasLines()))},
			{Keys: "f/b", Desc: "step"},
			{Keys: "g", Desc: "run"},
			{Keys: "n", Desc: "new"},
			{Keys: "k", Desc: "clear"},
			{Keys: "ctrl+n", Desc: "log"},
			{Keys: "ctrl+c", Desc: "quit"},
		}
	} else {
		cmd = []editmode.Binding{
			{Keys: "e", Desc: "edit"},
			{Keys: "n", Desc: "new"},
			{Keys: "c", Desc: "continue"},
			{Keys: "m", Desc: "info"},
			{Keys: "k", Desc: "clear"},
			{Keys: "l", Desc: "fit"},
			{Keys: "t", Desc: "box"},
			{Keys: "r", Desc: "reason:" + strings.TrimSpace(m.thinkModeLabel())},
			{Keys: "f/b", Desc: "step"},
			{Keys: "g", Desc: "run"},
			{Keys: "ctrl+n", Desc: "log"},
			{Keys: "< / >", Desc: "power"},
			{Keys: "ctrl+c", Desc: "quit"},
		}
	}
	return editmode.Keymap{Edit: edit, Command: cmd}
}

// logPageSize is the per-page scroll distance used by pgup/pgdown in the
// log overlay — matched to the visible content height so a single page
// advance moves the view a full screen.
func (m model) logPageSize() int {
	h := m.height - 6
	if h < 1 {
		return 1
	}
	return h
}

// logScrollBy returns a new logTop after moving by delta lines. A scroll
// up from follow-tail mode anchors the view at the current tail so
// incoming "done" lines do not slide what the user is reading; scrolling
// back to the bottom re-enables follow mode (-1).
func (m model) logScrollBy(delta int) int {
	total, innerH := m.logVisibleMetrics()
	maxTop := total - innerH
	if maxTop < 0 {
		maxTop = 0
	}
	top := m.logTop
	if top < 0 {
		top = maxTop
	}
	top += delta
	if top < 0 {
		top = 0
	}
	if top >= maxTop {
		return -1
	}
	return top
}

// logVisibleMetrics returns (total wrapped lines, visible content height).
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
	return len(wrapText(strings.Join(lines, "\n"), innerW)), innerH
}

// logOverlay renders the ctrl+n popup — recent libds4 diagnostics captured
// by the in-memory ring buffer — centered over a blank full-screen area.
// When m.logTop == -1 the view follows the tail; any scroll back anchors
// it at an absolute line so incoming "done" lines do not shift the view.
// esc closes; ctrl+c/q quits.
func (m model) logOverlay() string {
	lines := m.logBuf.Lines()
	if len(lines) == 0 {
		lines = []string{infoDimStyle.Render("(no libds4 diagnostics yet)")}
	}
	innerW := m.width - 6
	if innerW < 10 {
		innerW = 10
	}
	innerH := m.height - 6
	if innerH < 1 {
		innerH = 1
	}
	wrapped := wrapText(strings.Join(lines, "\n"), innerW)

	total := len(wrapped)
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
	body := strings.Join(wrapped[start:end], "\n")

	mode := "TAIL"
	if !following {
		mode = "FROZEN"
	}
	hint := fmt.Sprintf("  up/down · pgup/pgdown scroll · esc close   [%s %d/%d]",
		mode, start, maxTop)
	body += "\n\n" + infoDimStyle.Render(hint)

	box := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		MaxWidth(m.width).
		MaxHeight(m.height).
		Render(body), "ds4 · log")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// infoOverlay renders the m-key popup — model, metrics, and directory info —
// centered over a blank full-screen area.
func (m model) infoOverlay() string {
	ctxPct := 0
	if m.ctxSize > 0 {
		ctxPct = m.ctxPos * 100 / m.ctxSize
	}
	var b strings.Builder
	head := func(s string) { b.WriteString(infoHeadStyle.Render(s) + "\n") }
	row := func(k, v string) { b.WriteString(fmt.Sprintf("  %-9s %s\n", k, v)) }

	head("Model")
	if m.engine != nil {
		row("shape", m.engine.ModelName())
		row("shape id", strconv.Itoa(m.engine.ModelID()))
		row("gpu power", fmt.Sprintf("%d%%", m.engine.Power()))
	} else {
		p := m.engOpts.PowerPercent
		if p == 0 {
			p = 100
		}
		row("gpu power", fmt.Sprintf("%d%%", p))
	}
	row("file", filepath.Base(m.modelPath))
	row("path", m.modelPath)
	mtpDisplay := "--"
	mtpActive := false
	if m.hasMTP {
		mtpActive = m.mtpDraft > 1
		state := "active"
		if !mtpActive {
			state = "draft=1 (disabled)"
		}
		mtpDisplay = fmt.Sprintf("%s (%d draft) · %s", filepath.Base(m.mtpPath), m.mtpDraft, state)
	} else if m.mtpPath != "" {
		mtpDisplay = fmt.Sprintf("%s (not loaded)", filepath.Base(m.mtpPath))
	}
	row("mtp", mtpDisplay)
	row("backend", m.backend)
	row("context", fmt.Sprintf("%d tokens", m.ctxSize))
	row("reasoning", strings.TrimSpace(m.thinkModeLabel()))
	b.WriteString("\n")

	head("Metrics — last turn")
	row("tokens", strconv.Itoa(m.tokenCount))
	row("speed", fmt.Sprintf("%.1f tok/s", m.decodeSpeed()))
	row("ttft", fmtDuration(m.ttft()))
	row("gen time", fmtDuration(m.genTime()))
	row("ctx used", fmt.Sprintf("%d / %d  (%d%%)", m.ctxPos, m.ctxSize, ctxPct))
	row("canvas", fmt.Sprintf("%d × %d", m.canvasW, m.canvasH))
	b.WriteString("\n")

	head("Directory")
	row("working", m.workDir)
	row("log", filepath.Join(m.workDir, "glyphpad.log"))
	if m.hasMTP {
		b.WriteString("\n")
		head("MTP Diagnostics")
		row("hint", "DS4_MTP_TIMING=1  per-step timing → stderr")
		row("hint", "DS4_MTP_SPEC_LOG=1  miss logging → stderr")
		row("hint", "DS4_MTP_CONF_LOG=1  confidence log → stderr")
	}
	b.WriteString("\n" + infoDimStyle.Render("  press any key to close"))

	box := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		MaxWidth(m.width).
		MaxHeight(m.height).
		Render(b.String()), "ds4 · info")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// titledBox splices a title into the top border of a lipgloss-rendered box,
// e.g. ╭───────╮ becomes ╭─ Title ─╮. The box must have a plain (uncolored)
// border so the top line is plain runes. Box dimensions are preserved.
func titledBox(rendered, title string) string {
	if title == "" {
		return rendered
	}
	lines := strings.SplitN(rendered, "\n", 2)
	top := []rune(lines[0])
	label := []rune(" " + title + " ")
	if len(top) < len(label)+4 { // need ╭─ <label> ─╮
		return rendered
	}
	newTop := string(top[:2]) + titleStyle.Render(string(label)) + string(top[2+len(label):])
	if len(lines) == 2 {
		return newTop + "\n" + lines[1]
	}
	return newTop
}

// windowLines returns up to `height` lines, scrolled so index focus is visible.
func windowLines(lines []string, focus, height int) string {
	if height < 1 {
		height = 1
	}
	if len(lines) <= height {
		return strings.Join(lines, "\n")
	}
	start := focus - height/2
	if start < 0 {
		start = 0
	}
	if maxStart := len(lines) - height; start > maxStart {
		start = maxStart
	}
	return strings.Join(lines[start:start+height], "\n")
}

// ── generation ────────────────────────────────────────────────────────────────

func (m model) systemPrompt() string {
	w, h := m.canvasW, m.canvasH // the locked canvas size (updated by ctrl+l)
	if w < 20 {
		w = 60
	}
	if h < 5 {
		h = 20
	}
	return fmt.Sprintf(`You are a terminal artist and conversational assistant.

Respond with ONLY a JSON object containing exactly two string fields, in this order:
  "canvas":      a list of drawing commands that paint a picture for the user's message.
  "description": your conversational reply.

The canvas grid is %d columns wide — valid X is 0 to %d — and %d rows tall — valid Y is 0 to %d.
Coordinates OUTSIDE that range are discarded, so stay inside it. Center your drawing near column %d, row %d, and keep it small enough to fit.

Inside the "canvas" string, write one command per line, separated by \n. The commands:
  color NAME            set the pen color for every command that follows
  clear                 erase the whole canvas (discard everything drawn above)
  poke X Y G            place a single glyph G at (X,Y)
  htext X Y TEXT        write TEXT left-to-right from (X,Y)
  vtext X Y TEXT        write TEXT top-to-bottom from (X,Y)
  line X1 Y1 X2 Y2 G    draw a line of glyph G between two points
  box X Y W H           draw a box-drawing frame, top-left (X,Y), size W by H
  rect X Y W H G        draw a rectangle outline of glyph G
  fill X Y W H G        fill a W by H area with glyph G
  circle X Y R G        draw a circle outline of glyph G, center (X,Y) radius R
  disc X Y R G          draw a filled circle of glyph G
Write G and TEXT as bare glyphs — do NOT wrap them in quotes. Prefer box/line/circle/disc/fill over placing shapes glyph-by-glyph. Anything after a ; on a line is a comment and is ignored.
NAME may be a common color name (red, orange, yellow, gold, lime, green, teal, cyan, blue, navy, purple, pink, magenta, brown, white, gray, black, ...) or a #rrggbb hex code. Use color freely and change the pen often to make the picture vivid.

You place every piece by coordinate, so you never hand-align a grid — just emit many commands. Use a rich variety of glyphs: box ─│╭╮╯╰┌┐└┘═║, blocks █▓▒░▄▀, shapes ●○◆◇▲▼, stars ★✦, arrows ←→↑↓, emoji 🐱🔥🌟 (emoji span 2 columns).

Think briefly, then output the JSON — do NOT plan or sketch the drawing in your reasoning. Output ONLY the JSON object, no preamble.
Example: {"canvas":"color cyan\nbox 1 0 16 7\ncolor yellow\nhtext 4 2 hello\ncolor red\ndisc 8 4 1 ●","description":"A small demo."}`,
		w, w-1, h, h-1, w/2, h/2)
}

func (m model) generate(ctx context.Context, ch chan<- tea.Msg) {
	defer close(ch)

	// done sends the terminal message; ctxPos is read here (the generate
	// goroutine owns the session), never from the render goroutine.
	done := func(err error) {
		ch <- bubble.DoneMsg{Err: err, CtxPos: m.session.Pos()}
	}

	tokens, err := m.engine.NewTokens(nil)
	if err != nil {
		done(err)
		return
	}

	steps := []func() error{
		func() error { return m.engine.ChatBegin(tokens) },
		func() error { return m.engine.ChatAppendMessage(tokens, "system", m.systemPrompt()) },
	}
	for _, msg := range m.history {
		msg := msg
		steps = append(steps, func() error {
			return m.engine.ChatAppendMessage(tokens, msg.role, msg.content)
		})
	}
	steps = append(steps, func() error {
		return m.engine.ChatAppendAssistantPrefix(tokens, m.thinkMode)
	})

	for _, step := range steps {
		if err := step(); err != nil {
			tokens.Free()
			done(err)
			return
		}
	}

	opts := ds4.GenerateOptions{
		MaxTokens: 8192,
		StopOnEOS: true,
	}
	opts.OnToken = func(token int) {
		if text, err := m.engine.TokenText(token); err == nil {
			select {
			case ch <- bubble.TokenMsg(text):
			default:
				// Channel full — drop token so Continue can check context.
			}
		}
	}
	opts.Context = ctx

	gen := ds4.Generator{Engine: m.engine, Session: m.session}
	_, genErr := gen.GenerateTokens(tokens, opts)
	tokens.Free()
	done(genErr)
}

func (m model) generateContinue(ctx context.Context, ch chan<- tea.Msg) {
	defer close(ch)

	done := func(err error) {
		ch <- bubble.DoneMsg{Err: err, CtxPos: m.session.Pos()}
	}

	opts := ds4.GenerateOptions{
		MaxTokens: 8192,
		StopOnEOS: true,
	}
	opts.OnToken = func(token int) {
		if text, err := m.engine.TokenText(token); err == nil {
			select {
			case ch <- bubble.TokenMsg(text):
			default:
				// Channel full — drop token so Continue can check context.
			}
		}
	}
	opts.Context = ctx

	gen := ds4.Generator{Engine: m.engine, Session: m.session}
	_, genErr := gen.Continue(opts)
	done(genErr)
}
