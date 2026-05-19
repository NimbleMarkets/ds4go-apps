package main

import (
	"context"
	"encoding/json"
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
	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ntcharts/v2/canvas"
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

type tokenMsg string
type submitMsg struct{ text string }
type stepTickMsg struct{}
type doneMsg struct {
	err    error
	ctxPos int // session token position, snapshotted in the generate goroutine
}

// ── model ────────────────────────────────────────────────────────────────────

type model struct {
	width, height    int
	canvasW, canvasH int // locked canvas content size — the LLM's coordinate space

	engine    *ds4.Engine
	session   *ds4.Session
	modelPath string
	mtpPath   string
	hasMTP    bool
	mtpDraft  int
	backend   string
	workDir   string
	showInfo  bool // ctrl+m info overlay is up

	history    []chatMsg
	rawBuf     []byte // raw LLM response for the current turn
	tokenCh    chan tea.Msg
	genCtx     context.Context
	genCancel  context.CancelFunc
	generating bool
	statusText string
	errText    string

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

func newModel(engine *ds4.Engine, session *ds4.Session, modelPath, mtpPath string, hasMTP bool, mtpDraft int, backend string, logger *log.Logger, debug bool) model {
	ti := textinput.New()
	ti.Placeholder = "Ask anything..."
	ti.Focus()

	wd, _ := os.Getwd()
	return model{
		engine:       engine,
		session:      session,
		modelPath:    modelPath,
		mtpPath:      mtpPath,
		hasMTP:       hasMTP,
		mtpDraft:     mtpDraft,
		backend:      backend,
		workDir:      wd,
		parser:       NewParser(),
		canvas:       canvas.New(40, 20),
		input:        ti,
		statusText:   "Ready",
		showThinking: true,
		thinkBoxH:    defaultThinkBox,
		thinkMode:    ds4.ThinkNone,
		stepN:        -1,
		ctxSize:      session.Ctx(),
		logger:       logger,
		debug:        debug,
	}
}

// ── BubbleTea interface ───────────────────────────────────────────────────────

func (m model) Init() tea.Cmd { return nil }

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
		if m.showInfo { // info overlay is up — any key dismisses it
			if s := msg.String(); s == "ctrl+c" || s == "ctrl+q" {
				return m, tea.Quit
			}
			m.showInfo = false
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit

		case "esc":
			if m.generating && m.genCancel != nil {
				m.genCancel()
				m.statusText = "Aborting..."
			}

		case "ctrl+m": // info overlay: model / metrics / directory
			m.showInfo = true

		case "ctrl+t":
			m.showThinking = !m.showThinking
			m = m.resize()

		case "ctrl+r":
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

		case "shift+up":
			if m.showThinking {
				m.thinkBoxH++
				m = m.resize()
			}

		case "shift+down":
			if m.showThinking {
				m.thinkBoxH--
				m = m.resize()
			}

		case "ctrl+f": // step forward one command
			m.running = false
			total := len(m.parser.CanvasLines())
			if m.stepN >= 0 && m.stepN < total {
				m.stepN++
				if m.stepN >= total {
					m.stepN = -1
				}
				m, _ = m.redrawCanvas()
			}

		case "ctrl+b": // step back one command
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

		case "ctrl+g": // run: animate the command list
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

		case "ctrl+l": // re-fit the canvas to the current window size
			m = m.lockCanvas()

		case "ctrl+k": // clear the canvas, output, and conversation
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

		case "ctrl+n": // new prompt — clear and re-enable the input
			m.input.SetValue("")
			cmds = append(cmds, m.input.Focus())

		case "ctrl+e": // edit the locked prompt
			if !m.input.Focused() && !m.generating {
				cmds = append(cmds, m.input.Focus())
			}

		case "enter":
			if m.input.Focused() && !m.generating {
				text := strings.TrimSpace(m.input.Value())
				if text != "" {
					m.input.Blur() // lock the sent prompt in place
					cmds = append(cmds, func() tea.Msg { return submitMsg{text} })
				}
			}

		default:
			if m.input.Focused() { // ignore typing while a sent prompt is locked
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				cmds = append(cmds, cmd)
			}
		}

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
		m.genCtx, m.genCancel = context.WithCancel(context.Background())
		ch := make(chan tea.Msg, 64)
		m.tokenCh = ch
		go m.generate(ch)
		cmds = append(cmds, waitMsg(ch))

	case tokenMsg:
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
		cmds = append(cmds, waitMsg(m.tokenCh))

	case doneMsg:
		m.generating = false
		if m.genCtx != nil && m.genCtx.Err() == context.Canceled {
			m.statusText = "Aborted"
		} else {
			m.statusText = "Ready"
		}
		if m.genCancel != nil {
			m.genCancel()
			m.genCancel = nil
		}
		m.genCtx = nil
		m.genEnd = time.Now()
		m.ctxPos = msg.ctxPos
		if msg.err != nil {
			m.errText = msg.err.Error()
			if m.statusText != "Aborted" {
				m.statusText = "Error"
			}
			m.logger.Printf("[ERROR] %v", msg.err)
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
		m.tokenCh = nil

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

func waitMsg(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
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
		status = "Error: " + m.errText
	}
	canvasInfo := fmt.Sprintf("canvas %d×%d", m.canvasW, m.canvasH)
	if fw, fh := m.fitCanvasSize(); fw != m.canvasW || fh != m.canvasH {
		canvasInfo += " (ctrl+l)" // window changed — ctrl+l would re-fit
	}
	headerLeft := fmt.Sprintf(" ds4 TUI │ %s │ %s │ %s", modelName, canvasInfo, status)
	headerRight := m.metricsText()
	if m.hasMTP {
		if m.mtpDraft > 1 {
			headerRight += fmt.Sprintf(" · MTP · %d", m.mtpDraft)
		} else {
			headerRight += " · MTP (off)"
		}
	}
	headerContent := headerLeft
	if headerRight != "" {
		headerRight += " "
		if lipgloss.Width(headerLeft)+lipgloss.Width(headerRight)+1 <= m.width {
			pad := m.width - lipgloss.Width(headerLeft) - lipgloss.Width(headerRight)
			headerContent = headerLeft + strings.Repeat(" ", pad) + headerRight
		}
	}
	header := lipgloss.NewStyle().
		Width(m.width).
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

	sections := []string{header, middle}

	// Thinking box (full width, toggleable with ctrl+t)
	if m.showThinking {
		think := tailLines(wrapText(m.thinkText, m.width-2), m.thinkBoxH)
		if think == "" {
			switch {
			case m.generating:
				think = "..."
			case m.thinkMode == ds4.ThinkNone:
				think = "(reasoning off — ctrl+r to enable)"
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
		promptTitle = "Sent — ctrl+e edit · ctrl+n new"
	}
	inputPanel := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(m.width).
		Height(inputH).
		MaxHeight(inputH).
		Render(m.input.View()), promptTitle)
	sections = append(sections, inputPanel)

	// Footer help bar (shows step position while stepping)
	help := fmt.Sprintf(" ctrl+e edit · ctrl+n new · ctrl+m info · ctrl+k clear · ctrl+l fit · ctrl+t box · ctrl+r reason:%s · ctrl+f/b step · ctrl+g run · ctrl+c quit", m.thinkModeLabel())
	if m.stepN >= 0 {
		help = fmt.Sprintf(" STEP %d/%d · ctrl+f/b step · ctrl+g run · ctrl+n new · ctrl+k clear · ctrl+c quit",
			m.stepN, len(m.parser.CanvasLines()))
	}
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

// infoOverlay renders the ctrl+m popup — model, metrics, and directory info —
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
	row("log", filepath.Join(m.workDir, "play.log"))
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

func (m model) generate(ch chan tea.Msg) {
	defer close(ch)

	// done sends the terminal message; ctxPos is read here (the generate
	// goroutine owns the session), never from the render goroutine.
	done := func(err error) {
		ch <- doneMsg{err: err, ctxPos: m.session.Pos()}
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
			case ch <- tokenMsg(text):
			default:
				// Channel full — drop token so Continue can check context.
			}
		}
	}
	opts.Context = m.genCtx

	gen := ds4.Generator{Engine: m.engine, Session: m.session}
	_, genErr := gen.GenerateTokens(tokens, opts)
	tokens.Free()
	done(genErr)
}
