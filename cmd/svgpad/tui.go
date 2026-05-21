package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-playground/internal/ds4log"
	"github.com/NimbleMarkets/ds4go-playground/internal/editmode"
	"github.com/NimbleMarkets/ds4go-playground/internal/engineinit"
	"github.com/NimbleMarkets/ds4go-playground/internal/headerbar"
	svg "github.com/NimbleMarkets/ntcharts-svg/svg"
)

const (
	headerH      = 1
	footerH      = 1
	inputH       = 3 // 1 content line + 2 border chars
	defaultThink = 8
	minThink     = 2
	minPanelW    = 10

	// svgRenderEdge caps the rasterized bitmap's longer edge. The svg
	// widget defaults to 2000 (sized for its 64× zoom-inspect feature,
	// which svgpad does not use); rasterize time scales with pixel area,
	// so 768 is ~8× faster per SVG and still sharper than any terminal
	// pane can display.
	svgRenderEdge = 768
)

const defaultPrompt = "Generate an SVG of a pelican riding a bicycle"

// ── message types ────────────────────────────────────────────────────────────

type tokenMsg string
type submitMsg struct{ text string }
type doneMsg struct {
	err    error
	ctxPos int
}
type spinnerTickMsg struct{}
type yoloSubmitMsg struct{ text string }
type toolRoundMsg struct{}
type loadEntryMsg struct{}

// engineReadyMsg is delivered from the goroutine that opens the ds4 engine
// and session. The model holds nil engine/session until this lands.
type engineReadyMsg engineinit.Result

// widgetCache is an LRU cache of rasterized SVG bitmaps, keyed by
// filename. Caching the bitmap — rather than a svg.Model copy — lets a
// revisited entry render instantly via svg.Model.SetImage with no async
// reload, and avoids sharing the widget's generation counters across
// copies (which silently dropped or misrouted in-flight load results).
type widgetCache struct {
	maxSize int
	keys    []string // least-recently-used first
	images  map[string]image.Image
}

func newWidgetCache(maxSize int) *widgetCache {
	return &widgetCache{maxSize: maxSize, images: make(map[string]image.Image)}
}

// touch moves key to the most-recently-used end of the recency list.
func (c *widgetCache) touch(key string) {
	for i, k := range c.keys {
		if k == key {
			c.keys = append(c.keys[:i], c.keys[i+1:]...)
			break
		}
	}
	c.keys = append(c.keys, key)
}

// Get returns the cached bitmap for key, marking it most-recently-used.
func (c *widgetCache) Get(key string) (image.Image, bool) {
	img, ok := c.images[key]
	if !ok {
		return nil, false
	}
	c.touch(key)
	return img, true
}

// Put stores a bitmap under key, evicting the least-recently-used entry
// when a new key would push the cache over capacity.
func (c *widgetCache) Put(key string, img image.Image) {
	if _, exists := c.images[key]; !exists && len(c.keys) >= c.maxSize {
		oldest := c.keys[0]
		c.keys = c.keys[1:]
		delete(c.images, oldest)
	}
	c.images[key] = img
	c.touch(key)
}

// svgEntry tracks one generated image in the session.
type toolCallEntry struct {
	round  int
	name   string
	args   string
	result string
}

type svgEntry struct {
	filename    string
	prompt      string
	text        string
	title       string // from the enrichment pass (or pre-existing on-disk metadata)
	desc        string
	keywords    string
	model       string    // model name stamped into <ai:model> at save time
	generatedAt time.Time // matches <ai:generatedAt> in the saved file
	svgData     []byte
	toolCalls   []toolCallEntry
}

func spinnerTick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// openEngineCmd opens the ds4 engine on the goroutine bubbletea spawns
// for this Cmd. It is dispatched both on first user submit and on
// re-acquisition after a manual release. The library handle, engine
// options, and ctx size are captured by value so the goroutine can
// run independently of any later model mutation.
func openEngineCmd(lib *ds4.Library, opts ds4.EngineOptions, ctxSize int) tea.Cmd {
	return func() tea.Msg {
		return engineReadyMsg(engineinit.Open(lib, opts, ctxSize))
	}
}

// ── model ────────────────────────────────────────────────────────────────────

type panelFocus int

const (
	focusInput panelFocus = iota
	focusThinking
	focusTools
)

type model struct {
	width, height int

	engine       *ds4.Engine  // nil until engineReadyMsg
	session      *ds4.Session
	lib          *ds4.Library      // resolved in main, used by Init's goroutine
	engOpts      ds4.EngineOptions // captured to fire async open
	lifecycle engineLifecycle // engine state machine, drives badge + transitions
	engineErr    error             // set when StatusError
	modelPath    string
	mtpPath      string
	hasMTP       bool
	mtpDraft     int
	backend      string
	workDir      string
	showInfo  bool
	showLog   bool           // libds4 log overlay is up (ctrl+n)
	logBuf    *ds4log.Buffer // captured libds4 diagnostics
	logTop    int            // absolute first-visible line; -1 = follow tail

	history    []ds4.ChatMessage
	rawBuf     []byte // raw LLM response for the current turn
	tokenCh    chan tea.Msg
	genCtx     context.Context
	genCancel  context.CancelFunc
	generating bool
	statusText string
	errText    string

	svgWidget svg.Model

	input textinput.Model

	showThinking   bool
	thinkBoxH      int
	thinkBoxW      int // width of the thinking panel (divider position)
	thinkText      string
	thinkMode      ds4.ThinkMode
	thinkScroll    int
	thinkAutoScroll bool // when true, scroll follows new content

	showHelp bool

	panelFocus panelFocus
	toolScroll int
	spinnerFrame int

	entries    []svgEntry
	entryIndex int
	yoloMode   bool
	yoloCount  int
	outputText string
	cache      *widgetCache

	tools         *ds4.ToolRegistry
	toolRounds    int
	maxToolRounds int
	toolCalls     []toolCallEntry

	totalToolCalls  int
	totalToolRounds int
	toolCallCounts  map[string]int // per-tool cumulative call count

	autoCorrectCount int
	maxAutoCorrect   int

	// metrics
	genStart       time.Time
	jobStart       time.Time // set once on initial submit, never reset between tool rounds
	firstTokenTime time.Time
	genEnd         time.Time
	tokenCount     int
	ctxPos         int
	ctxSize        int
	elapsedText    string // updated at spinner tick rate (200ms)
	speedText      string // updated at spinner tick rate (200ms)

	logger *log.Logger
	debug  bool
}

// scanExistingSVGs reads all *.svg files in dir, sorts them by modification
// time (newest first), and returns them as svgEntry values.
func scanExistingSVGs(dir string, logger *log.Logger) []svgEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Printf("[SCAN] read dir %q: %v", dir, err)
		return nil
	}
	var files []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".svg") {
			files = append(files, e)
		}
	}
	logger.Printf("[SCAN] found %d svg files in %q", len(files), dir)
	sort.Slice(files, func(i, j int) bool {
		ii, _ := files[i].Info()
		ji, _ := files[j].Info()
		return ii.ModTime().After(ji.ModTime())
	})
	var result []svgEntry
	for _, f := range files {
		path := filepath.Join(dir, f.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			logger.Printf("[SCAN] skip %s: %v", f.Name(), err)
			continue
		}
		// If this file was enriched in a previous session, surface every
		// field we stamped into it so navigating to it shows the same
		// info as a freshly enriched entry — without a fresh LLM pass.
		md := parseSVGMetadata(string(data))
		result = append(result, svgEntry{
			filename:    f.Name(),
			svgData:     data,
			title:       md.title,
			desc:        md.desc,
			keywords:    md.keywords,
			prompt:      md.prompt,
			model:       md.model,
			generatedAt: md.generatedAt,
		})
	}
	logger.Printf("[SCAN] loaded %d entries", len(result))
	return result
}

func newModel(lib *ds4.Library, engOpts ds4.EngineOptions, ctxSize int, modelPath, mtpPath, backend string, logger *log.Logger, logBuf *ds4log.Buffer, debug bool) model {
	ti := textinput.New()
	ti.Placeholder = defaultPrompt
	ti.SetValue(defaultPrompt)
	ti.Focus()

	wd, _ := os.Getwd()
	existing := scanExistingSVGs(wd, logger)
	reg := ds4.NewToolRegistry()
	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "svg_validate",
		Description: "Validate an SVG markup string and report any issues with XML well-formedness, missing attributes, or structural problems. Call this if your SVG may be malformed to get diagnostic feedback.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"svg":{"type":"string","description":"The SVG markup to validate"}},"required":["svg"]}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		var params struct {
			SVG string `json:"svg"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return "", err
		}
		return validateSVGDetailed(params.SVG), nil
	})

	return model{
		lib:           lib,
		engOpts:       engOpts,
		lifecycle:     engineLifecycle{status: engineinit.StatusDormant},
		modelPath:     modelPath,
		mtpPath:       mtpPath,
		backend:       backend,
		workDir:       wd,
		input:         ti,
		statusText:    "GPU initializing…",
		ctxSize:       ctxSize,
		logger:        logger,
		debug:         debug,
		logBuf:        logBuf,
		logTop:        -1, // follow the tail by default
		svgWidget:     svg.NewWithConfig(svg.Config{Cols: 80, Rows: 24, RenderEdge: svgRenderEdge}),
		showThinking:  true,
		thinkBoxH:     defaultThink,
		thinkBoxW:     0, // set to 75% of width on first render
		thinkMode:     ds4.ThinkNone,
		cache:          newWidgetCache(50),
		tools:          reg,
		maxToolRounds:  3,
		maxAutoCorrect: 2,
		toolCallCounts: make(map[string]int),
		panelFocus:     focusInput,
		entries:        existing,
		entryIndex:     len(existing) - 1,
	}
}

// ── BubbleTea interface ───────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	// The engine is opened lazily on first prompt submit. Startup is
	// Dormant: the viewer panel and entry navigation work without any
	// model loaded.
	return m.svgWidget.Init()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resize()
		cmds = append(cmds, m.svgWidget.SetSize(m.svgWidth(), m.svgHeight()))
		m.logger.Printf("[SIZE] width=%d height=%d entries=%d", m.width, m.height, len(m.entries))
		// Defer entry loading so SetSize completes first.
		if len(m.entries) > 0 {
			cmds = append(cmds, tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
				return loadEntryMsg{}
			}))
		}

	case loadEntryMsg:
		if len(m.entries) > 0 {
			cmd := m.loadEntryCmd()
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case tea.KeyPressMsg:
		// Log overlay has its own scrollable input handling — only esc
		// (or quit) leaves it; the rest of the key set scrolls. Info and
		// help stay "any-key-dismisses" since they're not scrollable.
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
		if m.showInfo || m.showHelp {
			if s := msg.String(); s == "ctrl+c" || s == "ctrl+q" {
				return m, tea.Quit
			}
			m.showInfo = false
			m.showHelp = false
			return m, nil
		}

		// Keys that work in BOTH modes.
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit

		case "ctrl+n": // libds4 log overlay
			m.showLog = true
			return m, nil

		case "esc":
			if m.generating && m.genCancel != nil {
				m.genCancel()
				m.statusText = "Aborting..."
				return m, nil
			}
			if m.input.Focused() {
				m.input.Blur()
				m.panelFocus = focusThinking
			}
			return m, nil

		case "enter":
			if m.input.Focused() && !m.generating {
				if m.lifecycle.status != engineinit.StatusReady {
					m.statusText = "GPU initializing… please wait"
					return m, nil
				}
				text := strings.TrimSpace(m.input.Value())
				if text == "" {
					text = defaultPrompt
				}
				m.input.Blur()
				m.panelFocus = focusThinking
				m.yoloCount = 0 // manual reset
				cmds = append(cmds, func() tea.Msg { return submitMsg{text} })
			}
			return m, tea.Batch(cmds...)

		case "ctrl+plus", "ctrl+=":
			if m.showThinking {
				m.thinkBoxH++
				cmds = append(cmds, m.svgWidget.SetSize(m.svgWidth(), m.svgHeight()))
			}
			return m, tea.Batch(cmds...)

		case "ctrl+minus", "ctrl+-":
			if m.showThinking && m.thinkBoxH > minThink {
				m.thinkBoxH--
				cmds = append(cmds, m.svgWidget.SetSize(m.svgWidth(), m.svgHeight()))
			}
			return m, tea.Batch(cmds...)

		case "shift+up":
			if m.showThinking && m.thinkBoxH > minThink {
				m.thinkBoxH--
				cmds = append(cmds, m.svgWidget.SetSize(m.svgWidth(), m.svgHeight()))
			}
			return m, tea.Batch(cmds...)

		case "shift+down":
			if m.showThinking {
				m.thinkBoxH++
				cmds = append(cmds, m.svgWidget.SetSize(m.svgWidth(), m.svgHeight()))
			}
			return m, tea.Batch(cmds...)

		case "shift+left":
			if m.showThinking {
				if m.thinkBoxW == 0 {
					m.thinkBoxW = m.width * 3 / 4
				}
				if m.thinkBoxW > minPanelW+1 {
					m.thinkBoxW--
				}
			}
			return m, nil

		case "shift+right":
			if m.showThinking {
				if m.thinkBoxW == 0 {
					m.thinkBoxW = m.width * 3 / 4
				}
				m.thinkBoxW++
			}
			return m, nil

		case "tab":
			if m.showThinking {
				switch m.panelFocus {
				case focusInput:
					m.panelFocus = focusThinking
					m.input.Blur()
				case focusThinking:
					m.panelFocus = focusTools
				case focusTools:
					m.panelFocus = focusInput
					cmds = append(cmds, m.input.Focus())
				}
			} else {
				if m.input.Focused() {
					m.panelFocus = focusInput
					m.input.Blur()
				} else {
					m.panelFocus = focusInput
					cmds = append(cmds, m.input.Focus())
				}
			}
			return m, tea.Batch(cmds...)

		case "pgup":
			if !m.input.Focused() && len(m.entries) > 0 {
				if m.entryIndex > 0 {
					m.entryIndex--
					if cmd := m.loadEntryCmd(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
			return m, tea.Batch(cmds...)

		case "pgdown":
			if !m.input.Focused() && len(m.entries) > 0 {
				if m.entryIndex < len(m.entries)-1 {
					m.entryIndex++
					if cmd := m.loadEntryCmd(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
			return m, tea.Batch(cmds...)

		case "up":
			// Panel-aware scroll. When the input owns focus, fall through
			// to textinput handling below so its cursor history still works.
			switch m.panelFocus {
			case focusThinking:
				if m.thinkScroll > 0 {
					m.thinkScroll--
					m.thinkAutoScroll = false
				}
				return m, nil
			case focusTools:
				if m.toolScroll > 0 {
					m.toolScroll--
				}
				return m, nil
			}

		case "down":
			switch m.panelFocus {
			case focusThinking:
				m.thinkScroll++
				return m, nil
			case focusTools:
				m.toolScroll++
				return m, nil
			}
		}

		// In edit mode, everything else types into the box.
		if m.input.Focused() {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

		// Command mode: bare-letter commands.
		switch msg.String() {
		case "e":
			if !m.generating {
				m.panelFocus = focusInput
				cmds = append(cmds, m.input.Focus())
			}

		case "n":
			m.input.SetValue("")
			m.thinkText = ""
			m.panelFocus = focusInput
			cmds = append(cmds, m.input.Focus())

		case "m":
			m.showInfo = true

		case "?", "h":
			m.showHelp = true

		case "t":
			m.showThinking = !m.showThinking
			if !m.showThinking && m.panelFocus != focusInput {
				m.panelFocus = focusInput
				cmds = append(cmds, m.input.Focus())
			}
			cmds = append(cmds, m.svgWidget.SetSize(m.svgWidth(), m.svgHeight()))

		case "r":
			switch m.thinkMode {
			case ds4.ThinkNone:
				m.thinkMode = ds4.ThinkHigh
			case ds4.ThinkHigh:
				m.thinkMode = ds4.ThinkMax
			default:
				m.thinkMode = ds4.ThinkNone
			}
			m.logger.Printf("[REASONING] %s", m.thinkModeLabel())

		case "g":
			cmds = append(cmds, m.svgWidget.ToggleRenderMode())

		case "y":
			m.yoloMode = !m.yoloMode
			m.yoloCount = 0
			if m.yoloMode {
				m.statusText = "YOLO mode ON"
			} else {
				m.statusText = "YOLO mode OFF"
			}
			m.logger.Printf("[YOLO] mode=%v", m.yoloMode)
		}

	case metadataDoneMsg:
		base := filepath.Base(msg.filename)
		if msg.err != nil {
			m.logger.Printf("[META] %s failed: %v", base, msg.err)
			if !m.generating {
				m.statusText = strings.Replace(m.statusText, " · enriching", " · metadata failed", 1)
			}
		} else {
			m.logger.Printf("[META] %s enriched  title=%q", base, msg.title)
			if !m.generating {
				m.statusText = strings.Replace(m.statusText, " · enriching", " · enriched", 1)
			}
		}
		// Even on splice/write failure, the model may have produced a
		// usable title/desc — surface it on the matching entry. The
		// deterministic fields (model, generatedAt) come from the cmd
		// regardless of LLM output, so always copy them through too.
		for i := range m.entries {
			if m.entries[i].filename == base {
				if msg.title != "" {
					m.entries[i].title = msg.title
				}
				if msg.desc != "" {
					m.entries[i].desc = msg.desc
				}
				if msg.keywords != "" {
					m.entries[i].keywords = msg.keywords
				}
				if msg.model != "" {
					m.entries[i].model = msg.model
				}
				if !msg.generatedAt.IsZero() {
					m.entries[i].generatedAt = msg.generatedAt
				}
				break
			}
		}
		if m.entryIndex >= 0 && m.entryIndex < len(m.entries) &&
			m.entries[m.entryIndex].filename == base {
			m.outputText = entryText(m.entries[m.entryIndex])
		}
		return m, nil

	case engineReadyMsg:
		if msg.Err != nil {
			m.lifecycle.status = engineinit.StatusError
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
		m.lifecycle.status = engineinit.StatusReady
		m.statusText = "Ready"
		m.logger.Printf("[ENGINE] ready  mtp=%v mtpDraft=%d ctx=%d",
			m.hasMTP, m.mtpDraft, m.ctxSize)
		return m, nil

	case submitMsg:
		m.logger.Printf("[USER] %s", msg.text)
		m.history = append(m.history, ds4.ChatMessage{Role: "user", Content: msg.text})
		m.rawBuf = m.rawBuf[:0]
		m.thinkText = ""
		m.outputText = ""
		m.toolCalls = m.toolCalls[:0]
		m.thinkScroll = 0
		m.toolScroll = 0
		m.thinkAutoScroll = true
		m.panelFocus = focusThinking
		m.autoCorrectCount = 0
		m.generating = true
		m.statusText = fmt.Sprintf("Generate [0/%d]...", m.maxToolRounds)
		m.errText = ""
		m.toolRounds = 0
		m.jobStart = time.Now()
		m.genStart = m.jobStart
		m.firstTokenTime = time.Time{}
		m.genEnd = time.Time{}
		m.tokenCount = 0
		m.spinnerFrame = 0
		m.genCtx, m.genCancel = context.WithCancel(context.Background())
		cmds = append(cmds, spinnerTick())
		ch := make(chan tea.Msg, 64)
		m.tokenCh = ch
		go m.generate(ch)
		cmds = append(cmds, waitMsg(ch))

	case toolRoundMsg:
		m.rawBuf = m.rawBuf[:0]
		m.thinkText = ""
		m.outputText = ""
		m.generating = true
		m.statusText = fmt.Sprintf("Generate [%d/%d]...", m.toolRounds, m.maxToolRounds)
		m.errText = ""
		m.thinkAutoScroll = true
		m.genStart = time.Now() // per-round timer resets, jobStart does not
		m.firstTokenTime = time.Time{}
		m.genEnd = time.Time{}
		m.tokenCount = 0
		m.spinnerFrame = 0
		m.genCtx, m.genCancel = context.WithCancel(context.Background())
		cmds = append(cmds, spinnerTick())
		ch := make(chan tea.Msg, 64)
		m.tokenCh = ch
		go m.generate(ch)
		cmds = append(cmds, waitMsg(ch))

	case tokenMsg:
		text := string(msg)
		if m.firstTokenTime.IsZero() {
			m.firstTokenTime = time.Now()
		}
		m.tokenCount++
		m.rawBuf = append(m.rawBuf, text...)
		m.thinkText = extractThink(string(m.rawBuf))
		m.outputText = extractOutputText(string(m.rawBuf))

		cmds = append(cmds, waitMsg(m.tokenCh))

	case spinnerTickMsg:
		if m.generating {
			m.spinnerFrame++
			if !m.genStart.IsZero() {
				round := fmtDuration(time.Since(m.genStart))
				if !m.jobStart.IsZero() && m.toolRounds > 0 {
					total := fmtDuration(time.Since(m.jobStart))
					m.elapsedText = round + " / " + total
				} else {
					m.elapsedText = round
				}
			}
			if speed := m.decodeSpeed(); speed > 0 {
				m.speedText = fmt.Sprintf("%.1f tok/s", speed)
			}
			cmds = append(cmds, spinnerTick())
		}

	case yoloSubmitMsg:
		if m.yoloMode && !m.generating && m.yoloCount < 20 {
			cmds = append(cmds, func() tea.Msg { return submitMsg{msg.text} })
		}

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
		if msg.err != nil && msg.err != context.Canceled {
			m.errText = msg.err.Error()
			m.statusText = "Error"
		}
		m.logger.Printf("[DONE] %d tok  %.1f tok/s  ttft=%s  gen=%s  ctx=%d/%d",
			m.tokenCount, m.decodeSpeed(), fmtDuration(m.ttft()), fmtDuration(m.genTime()),
			m.ctxPos, m.ctxSize)

		// Check for tool calls in the assistant response.
		assistant, parseErr := m.tools.ParseAssistant(string(m.rawBuf), m.thinkMode != ds4.ThinkNone)
		rawPreview := string(m.rawBuf)
		if len(rawPreview) > 300 {
			rawPreview = rawPreview[:300] + "..."
		}
		m.logger.Printf("[PARSE] tools=%d parseErr=%v think=%v raw=%q", len(assistant.ToolCalls), parseErr, m.thinkMode != ds4.ThinkNone, rawPreview)
		if parseErr == nil && len(assistant.ToolCalls) > 0 && m.toolRounds < m.maxToolRounds && msg.err == nil {
			m.toolRounds++
			var toolNames []string
			for _, call := range assistant.ToolCalls {
				toolNames = append(toolNames, call.Name)
			}
			m.statusText = fmt.Sprintf("Running %s...", strings.Join(toolNames, ", "))
			m.logger.Printf("[TOOL] round=%d calls=%d", m.toolRounds, len(assistant.ToolCalls))
		for _, call := range assistant.ToolCalls {
			m.logger.Printf("[TOOL] arg %s=%q", call.Name, truncateString(call.Arguments, 500))
		}
			results, toolErr := m.tools.ExecuteToolCalls(context.Background(), assistant.ToolCalls)
			if toolErr != nil {
				m.statusText = "Tool error"
				m.errText = toolErr.Error()
				m.logger.Printf("[TOOL] error: %v", toolErr)
			} else {
				m.history = append(m.history, assistant)
				m.history = append(m.history, results...)
				for _, call := range assistant.ToolCalls {
					var res string
					for _, r := range results {
						if r.ToolCallID == call.ID {
							res = r.Content
							break
						}
					}
					m.toolCalls = append(m.toolCalls, toolCallEntry{
						round:  m.toolRounds,
						name:   call.Name,
						args:   truncateJSON(call.Arguments, 120),
						result: truncateString(res, 120),
					})
					m.toolCallCounts[call.Name]++
					m.logger.Printf("[TOOL] result id=%s content=%q", call.ID, res)
				}
				m.totalToolCalls += len(assistant.ToolCalls)
				m.totalToolRounds++
				cmds = append(cmds, func() tea.Msg { return toolRoundMsg{} })
				return m, tea.Batch(cmds...)
			}
		}
		m.toolRounds = 0

		// Append assistant message to history (so follow-up rounds see context).
		if parseErr == nil {
			m.history = append(m.history, assistant)
		} else {
			m.history = append(m.history, ds4.ChatMessage{Role: "assistant", Content: string(m.rawBuf)})
		}

		// Extract final artifacts.
		// Use parsed assistant content (DSML stripped) when available.
		var content string
		if parseErr == nil {
			content = assistant.Content
		} else {
			content = string(m.rawBuf)
		}
		svgData := extractSVG(content)
		outputText := extractOutputText(content)
		nextPrompt := extractPrompt(content)
		m.outputText = outputText

		// Determine the prompt for this generation.
		var currentPrompt string
		for i := len(m.history) - 1; i >= 0; i-- {
			if m.history[i].Role == "user" && m.history[i].ToolCallID == "" {
				currentPrompt = m.history[i].Content
				break
			}
		}

		// Auto-correction: if SVG is missing or invalid, feed back error and retry.
		if m.autoCorrectCount < m.maxAutoCorrect && msg.err == nil {
			var v string
			if len(svgData) == 0 {
				v = "no SVG markup found — your response must start with <svg and end with </svg>"
			} else {
				v = validateSVG(svgData)
			}
			if v != "valid" {
				m.autoCorrectCount++
				m.statusText = fmt.Sprintf("Fixing SVG (%d/%d) · %s", m.autoCorrectCount, m.maxAutoCorrect, v)
				m.logger.Printf("[AUTOCORRECT] #%d error=%q", m.autoCorrectCount, v)
				feedback := fmt.Sprintf("Your output was invalid: %s. Please output ONLY a corrected, complete SVG.", v)
				m.history = append(m.history, ds4.ChatMessage{Role: "user", Content: feedback})
				cmds = append(cmds, func() tea.Msg { return toolRoundMsg{} })
				return m, tea.Batch(cmds...)
			}
		}
		m.autoCorrectCount = 0

		// Feed the completed SVG to the widget, validate, and auto-save.
		var fname string
		if len(svgData) > 0 {
			cmds = append(cmds, m.svgWidget.SetSVGData("output.svg", svgData))
			v := validateSVG(svgData)
			if v == "valid" {
				m.statusText = "Ready · SVG valid"
			} else {
				m.statusText = "Ready · " + v
			}
			m.logger.Printf("[SVG] %s (%d bytes)", v, len(svgData))

			fname = fmt.Sprintf("svgpad.%s.svg", time.Now().Format("20060102_150405"))
			path := filepath.Join(m.workDir, fname)
			if err := os.WriteFile(path, svgData, 0644); err != nil {
				m.statusText += " · save failed"
				m.logger.Printf("[SAVE] failed: %v", err)
				fname = ""
			} else {
				m.statusText += " · saved " + fname
				m.logger.Printf("[SAVE] %s (%d bytes)", fname, len(svgData))
				// Fire the second LLM pass: write <title>/<desc>/<metadata>
				// into the saved file on a fresh session so the main
				// conversation isn't polluted.
				m.statusText += " · enriching"
				cmds = append(cmds, enrichMetadataCmd(
					m.engine, filepath.Base(m.modelPath), path, currentPrompt, svgData))
			}
		}

		// Record session entry.
		entry := svgEntry{
			filename:  fname,
			prompt:    currentPrompt,
			text:      outputText,
			svgData:   svgData,
			toolCalls: make([]toolCallEntry, len(m.toolCalls)),
		}
		copy(entry.toolCalls, m.toolCalls)
		m.entries = append(m.entries, entry)
		m.entryIndex = len(m.entries) - 1

		// Structured log entry.
		m.logger.Printf("[ENTRY] filename=%q prompt=%q text=%q", fname, currentPrompt, outputText)

		// YOLO trigger.
		if m.yoloMode && nextPrompt != "" && m.yoloCount < 20 {
			m.yoloCount++
			m.statusText = "YOLO · " + m.statusText
			m.logger.Printf("[YOLO] #%d next=%q", m.yoloCount, nextPrompt)
			cmds = append(cmds, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
				return yoloSubmitMsg{text: nextPrompt}
			}))
		}

	default:
		var cmd tea.Cmd
		m.svgWidget, cmd = m.svgWidget.Update(msg)
		cmds = append(cmds, cmd)
		if m.input.Focused() {
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func waitMsg(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// ── generation ───────────────────────────────────────────────────────────────

func (m model) generate(ch chan tea.Msg) {
	defer close(ch)

	done := func(err error) {
		ch <- doneMsg{err: err, ctxPos: m.session.Pos()}
	}

	prompt, err := m.tools.BuildPrompt(m.engine, m.systemPrompt(), m.history, m.thinkMode)
	if err != nil {
		done(err)
		return
	}
	defer prompt.Free()
	if m.debug {
		toolsSection, _ := m.tools.RenderToolsSection()
		m.logger.Printf("[PROMPT] len=%d tools=%q", prompt.Len(), toolsSection)
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
	_, genErr := gen.GenerateTokens(prompt, opts)
	done(genErr)
}

func (m model) systemPrompt() string {
	base := `You are an SVG artist. Your final answer must be valid, complete SVG markup.
When generating SVG, call the svg_validate tool with your markup before finalizing your answer.
Do not include explanations, markdown code blocks, or any text outside the SVG tags in your final answer.
Start with <svg and end with </svg>. Ensure the SVG has proper xmlns="http://www.w3.org/2000/svg".
If you are told your SVG has errors, fix them and output the corrected, complete SVG.`
	if m.yoloMode {
		base += `

After the SVG, you may include a brief description and a follow-up prompt in a <prompt> tag:
<prompt>your next creative idea here</prompt>
When you are done creating, omit the <prompt> tag.`
	}
	return base
}

// extractSVG pulls the first <svg>...</svg> block from a string.
func extractSVG(s string) []byte {
	start := strings.Index(s, "<svg")
	if start == -1 {
		return nil
	}
	end := strings.Index(s[start:], "</svg>")
	if end == -1 {
		return nil
	}
	return []byte(s[start : start+end+len("</svg>")])
}

// extractThink pulls the <think>...</think> content from a string.
func extractThink(s string) string {
	start := strings.Index(s, "<think>")
	if start == -1 {
		return ""
	}
	end := strings.Index(s[start:], "</think>")
	if end == -1 {
		return s[start+len("<think>"):]
	}
	return s[start+len("<think>") : start+end]
}

// extractPrompt pulls the first <prompt>...</prompt> block from raw buffer.
func extractPrompt(s string) string {
	start := strings.Index(s, "<prompt>")
	if start == -1 {
		return ""
	}
	end := strings.Index(s[start:], "</prompt>")
	if end == -1 {
		return ""
	}
	return s[start+len("<prompt>") : start+end]
}

// extractOutputText returns text that is NOT inside <think>, <svg>, or <prompt> tags.
func extractOutputText(s string) string {
	// Strip <think>...</think>
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "</think>")
		if end == -1 {
			s = strings.TrimSpace(s[:start])
			break
		}
		s = strings.TrimSpace(s[:start] + s[start+end+len("</think>"):])
	}
	// Strip <svg>...</svg>
	for {
		start := strings.Index(s, "<svg")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "</svg>")
		if end == -1 {
			s = strings.TrimSpace(s[:start])
			break
		}
		s = strings.TrimSpace(s[:start] + s[start+end+len("</svg>"):])
	}
	// Strip <prompt>...</prompt>
	for {
		start := strings.Index(s, "<prompt>")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "</prompt>")
		if end == -1 {
			s = strings.TrimSpace(s[:start])
			break
		}
		s = strings.TrimSpace(s[:start] + s[start+end+len("</prompt>"):])
	}
	return s
}

// validateSVG checks well-formedness and returns a short status string.
func validateSVG(data []byte) string {
	if len(data) == 0 {
		return "empty"
	}
	trim := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trim, "<svg") {
		return "missing <svg root"
	}
	decoder := xml.NewDecoder(strings.NewReader(trim))
	for {
		tok, err := decoder.Token()
		if err != nil {
			return "parse error: " + err.Error()
		}
		if tok == nil {
			break
		}
		if _, ok := tok.(xml.EndElement); ok {
			if decoder.InputOffset() >= int64(len(trim))-10 {
				break
			}
		}
	}
	return "valid"
}

// validateSVGDetailed performs thorough validation and returns a detailed report
// suitable for feeding back to the LLM via a tool result.
func validateSVGDetailed(svg string) string {
	svg = strings.TrimSpace(svg)
	if svg == "" {
		return "Error: empty SVG string"
	}

	var issues []string

	if !strings.HasPrefix(svg, "<svg") {
		issues = append(issues, "Missing <svg root element. The document must start with <svg.")
	}
	hasNS := strings.Contains(svg, `xmlns="http://www.w3.org/2000/svg"`) || strings.Contains(svg, `xmlns='http://www.w3.org/2000/svg'`)
	if !hasNS {
		issues = append(issues, `Missing xmlns="http://www.w3.org/2000/svg" attribute on the root <svg> element.`)
	}

	decoder := xml.NewDecoder(strings.NewReader(svg))
	var depth int
	for {
		tok, err := decoder.Token()
		if err != nil {
			issues = append(issues, fmt.Sprintf("XML parse error at byte %d: %v", decoder.InputOffset(), err))
			break
		}
		if tok == nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
			if depth < 0 {
				issues = append(issues, fmt.Sprintf("Unexpected closing tag </%s> at byte %d", t.Name.Local, decoder.InputOffset()))
			}
		}
	}
	if depth != 0 {
		issues = append(issues, fmt.Sprintf("Unclosed tags: depth=%d at end of document", depth))
	}

	if len(issues) == 0 {
		return "Valid: well-formed XML with correct SVG root element and namespace."
	}
	return "Invalid:\n" + strings.Join(issues, "\n")
}

// ── metrics ──────────────────────────────────────────────────────────────────

func (m model) ttft() time.Duration {
	if m.firstTokenTime.IsZero() || m.genStart.IsZero() {
		return 0
	}
	return m.firstTokenTime.Sub(m.genStart)
}

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

// headerMetrics returns a short right-aligned metric string for the header.
func (m model) headerMetrics() string {
	if m.genStart.IsZero() && len(m.entries) == 0 {
		return ""
	}
	var parts []string
	if len(m.entries) > 0 {
		parts = append(parts, fmt.Sprintf("[%d/%d]", m.entryIndex+1, len(m.entries)))
	}
	if m.generating && m.tokenCount > 0 {
		parts = append(parts, fmt.Sprintf("%d tok", m.tokenCount))
	}
	if m.ctxSize > 0 {
		ctxPct := m.ctxPos * 100 / m.ctxSize
		parts = append(parts, fmt.Sprintf("ctx %d%%", ctxPct))
	}
	if m.generating && m.speedText != "" {
		parts = append(parts, m.speedText)
	}
	if !m.generating {
		if speed := m.decodeSpeed(); speed > 0 {
			parts = append(parts, fmt.Sprintf("%.1f tok/s", speed))
		}
		if gen := m.genTime(); gen > 0 {
			parts = append(parts, fmt.Sprintf("gen %s", fmtDuration(gen)))
		}
	}
	return strings.Join(parts, " · ")
}

// thinkModeLabel returns a fixed-width (4-char) label for the reasoning mode.
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

// loadEntryCmd switches the viewer to the entry at m.entryIndex. When
// the entry's bitmap is cached it is installed directly (instant, no
// async work); otherwise a fresh load is started. The single svgWidget
// is mutated in place — never swapped for a cached copy.
func (m *model) loadEntryCmd() tea.Cmd {
	if m.entryIndex < 0 || m.entryIndex >= len(m.entries) {
		return nil
	}
	e := m.entries[m.entryIndex]
	m.thinkText = ""
	m.outputText = entryText(e)
	m.thinkScroll = 0
	m.toolScroll = 0
	m.toolCalls = make([]toolCallEntry, len(e.toolCalls))
	copy(m.toolCalls, e.toolCalls)
	if e.filename != "" {
		if img, ok := m.cache.Get(e.filename); ok {
			return m.svgWidget.SetImage(img)
		}
	}
	if len(e.svgData) > 0 {
		return m.svgWidget.SetSVGData(e.filename, e.svgData)
	}
	return nil
}

func (m model) svgWidth() int {
	w := m.width
	if w < 10 {
		w = 10
	}
	return w
}

// svgHeight returns the content height for the SVG widget, accounting for the
// thinking box when visible.
func (m model) svgHeight() int {
	h := m.height - headerH - footerH - inputH
	if m.showThinking {
		h -= m.thinkBoxH + 2 // + border
	}
	if h < 3 {
		h = 3
	}
	return h
}

// ── view ─────────────────────────────────────────────────────────────────────

func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	// Header — plain text to avoid lipgloss v2 truncation issues
	modelName := filepath.Base(m.modelPath)
	if lipgloss.Width(modelName) > 20 {
		modelName = modelName[:17] + "..."
	}
	status := m.statusText
	if m.errText != "" {
		status = "Error: " + m.errText
	} else if m.generating {
		if m.elapsedText != "" {
			status = m.statusText + " " + m.elapsedText + " " + m.bicycleSpinner()
		} else {
			status = m.statusText + " " + m.bicycleSpinner()
		}
	}
	header := headerbar.Layout(
		m.width,
		fmt.Sprintf(" svgpad │ %s │ ", modelName),
		status,
		m.headerMetrics(),
		engineinit.Badge(m.lifecycle.status),
	)

	// Footer
	// Footer help bar — swaps with mode (edit vs command) via the shared
	// editmode keymap; the full key reference lives in the ? overlay.
	footerContent := " " + m.keymap().FooterText(m.input.Focused())
	footer := lipgloss.NewStyle().
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("252")).
		Width(m.width).
		Render(footerContent)

	// SVG area
	svgH := m.svgHeight()
	svgW := m.svgWidth()

	// Render the SVG widget into a panel.
	svgPanel := m.svgWidget.View().Content
	if strings.TrimSpace(svgPanel) == "" {
		svgPanel = " No SVG yet — press Enter to generate"
	} else if m.entryIndex >= 0 && m.entryIndex < len(m.entries) {
		// Cache the rasterized bitmap so revisiting this entry is
		// instant. Image() is nil until the async render completes, so
		// half-loaded entries are simply skipped until they finish.
		fname := m.entries[m.entryIndex].filename
		if fname != "" {
			if img := m.svgWidget.Image(); img != nil {
				m.cache.Put(fname, img)
			}
		}
	}
	svgLines := strings.Split(svgPanel, "\n")
	for len(svgLines) < svgH {
		svgLines = append(svgLines, strings.Repeat(" ", svgW))
	}
	if len(svgLines) > svgH {
		svgLines = svgLines[:svgH]
	}
	for i := range svgLines {
		if lipgloss.Width(svgLines[i]) < svgW {
			svgLines[i] += strings.Repeat(" ", svgW-lipgloss.Width(svgLines[i]))
		} else if lipgloss.Width(svgLines[i]) > svgW {
			svgLines[i] = svgLines[i][:svgW]
		}
	}
	svgContent := strings.Join(svgLines, "\n")

	// Thinking / output box + tool calls panel
	var sections []string
	sections = append(sections, header)
	if m.showThinking {
		var thinkContent string
		if m.thinkText != "" && m.outputText != "" {
			thinkContent = m.thinkText + "\n─────────────────\n" + m.outputText
		} else if m.thinkText != "" {
			thinkContent = m.thinkText
		} else if m.outputText != "" {
			thinkContent = m.outputText
		} else if m.generating && len(m.rawBuf) > 0 {
			if strings.Contains(string(m.rawBuf), "<svg") {
				thinkContent = fmt.Sprintf("Generating SVG... (%d bytes so far)", len(m.rawBuf))
			} else {
				preview := string(m.rawBuf)
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				thinkContent = preview
			}
		} else {
			if m.thinkMode == ds4.ThinkNone {
				thinkContent = "(reasoning off — r to enable in command mode)"
			} else {
				thinkContent = "…"
			}
		}

		// Left "Image" info panel takes a narrow fixed slice; think/tool
		// share what remains, divided by m.thinkBoxW. Skip the info
		// panel when the terminal is too narrow to host three columns.
		infoW := 30
		if max := m.width / 3; infoW > max {
			infoW = max
		}
		if infoW < 18 {
			infoW = 0
		}
		if m.width-infoW < 2*minPanelW {
			infoW = 0
		}
		remaining := m.width - infoW

		if m.thinkBoxW == 0 {
			m.thinkBoxW = remaining * 3 / 4
		}
		if m.thinkBoxW < minPanelW {
			m.thinkBoxW = minPanelW
		}
		if m.thinkBoxW > remaining-minPanelW {
			m.thinkBoxW = remaining - minPanelW
		}
		thinkW := m.thinkBoxW
		toolW := remaining - thinkW
		thinkContentW := thinkW - 2 // borders
		toolContentW := toolW - 2

		// Compute wrapped line count for auto-scroll and bottom-detection.
		wrapped := lipgloss.NewStyle().Width(thinkContentW).Render(thinkContent)
		totalLines := len(strings.Split(wrapped, "\n"))
		bottomScroll := totalLines - m.thinkBoxH
		if bottomScroll < 0 {
			bottomScroll = 0
		}

		// Auto-scroll to bottom unless user has manually scrolled up.
		// If they scroll back to the bottom, re-enable auto-scroll.
		if m.thinkAutoScroll {
			m.thinkScroll = bottomScroll
		} else if m.thinkScroll >= bottomScroll {
			m.thinkAutoScroll = true
			m.thinkScroll = bottomScroll
		}

		// Apply scroll offset, pre-wrap to content width, and truncate to exact height.
		thinkContent = wrapAndTruncate(thinkContent, thinkContentW, m.thinkBoxH, m.thinkScroll)
		toolContent := m.renderToolPanel()
		toolContent = wrapAndTruncate(toolContent, toolContentW, m.thinkBoxH, m.toolScroll)

		thinkBorderColor := lipgloss.Color("240")
		toolBorderColor := lipgloss.Color("240")
		if m.panelFocus == focusThinking {
			thinkBorderColor = lipgloss.Color("75")
		} else if m.panelFocus == focusTools {
			toolBorderColor = lipgloss.Color("75")
		}

		thinkingPanel := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(thinkBorderColor).
			Width(thinkW).MaxWidth(thinkW).
			Height(m.thinkBoxH + 2).MaxHeight(m.thinkBoxH + 2).
			Render(thinkContent)
		toolPanel := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(toolBorderColor).
			Width(toolW).MaxWidth(toolW).
			Height(m.thinkBoxH + 2).MaxHeight(m.thinkBoxH + 2).
			Render(toolContent)

		var topRow string
		if infoW > 0 {
			var infoContent string
			if m.entryIndex >= 0 && m.entryIndex < len(m.entries) {
				infoContent = renderEntryInfo(m.entries[m.entryIndex])
			}
			infoContent = wrapAndTruncate(infoContent, infoW-2, m.thinkBoxH, 0)
			infoPanel := lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("240")).
				Width(infoW).MaxWidth(infoW).
				Height(m.thinkBoxH + 2).MaxHeight(m.thinkBoxH + 2).
				Render(infoContent)
			topRow = lipgloss.JoinHorizontal(lipgloss.Top, infoPanel, thinkingPanel, toolPanel)
		} else {
			topRow = lipgloss.JoinHorizontal(lipgloss.Top, thinkingPanel, toolPanel)
		}
		sections = append(sections, topRow)
	}
	sections = append(sections, svgContent)

	// Input
	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		Width(m.width - 2).
		Height(inputH - 2)
	if m.input.Focused() {
		inputStyle = inputStyle.BorderForeground(lipgloss.Color("75"))
	}
	inputContent := inputStyle.Render(m.input.View())
	sections = append(sections, inputContent, footer)

	if m.showHelp {
		return m.helpOverlay()
	}
	if m.showInfo {
		return m.infoOverlay()
	}
	if m.showLog {
		return m.logOverlay()
	}
	out := strings.Join(sections, "\n")
	return out
}

func (m model) bicycleSpinner() string {
	bike := "🦤🚲"
	track := 10
	cycle := track * 2
	p := m.spinnerFrame % cycle
	if p > track {
		p = cycle - p
	}
	left := strings.Repeat(" ", p)
	right := strings.Repeat(" ", track-p)
	spinner := "[" + left + bike + right + "]"
	// Bright background so the emojis pop on dark terminals.
	return lipgloss.NewStyle().
		Background(lipgloss.Color("220")).
		Foreground(lipgloss.Color("0")).
		Bold(true).
		Render(spinner)
}

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

func (m model) renderToolPanel() string {
	var b strings.Builder

	// Catalog header
	headStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	b.WriteString(headStyle.Render("Catalog") + "\n")

	// List registered tools with call counts
	for _, schema := range m.tools.Schemas() {
		count := m.toolCallCounts[schema.Name]
		countStr := ""
		if count > 0 {
			countStr = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render(fmt.Sprintf(" (%d)", count))
		}
		b.WriteString(fmt.Sprintf("  %s%s\n", schema.Name, countStr))
	}

	// Call history
	if len(m.toolCalls) > 0 {
		b.WriteString("\n" + headStyle.Render("Calls") + "\n")
		for i, tc := range m.toolCalls {
			if i > 0 {
				b.WriteString("\n")
			}
			nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
			b.WriteString(nameStyle.Render(tc.name))
			b.WriteString(fmt.Sprintf("  round %d\n", tc.round))
			if tc.args != "" {
				b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  args: " + tc.args) + "\n")
			}
			if tc.result != "" {
				res := tc.result
				if strings.Contains(res, "Invalid:") {
					b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("  → " + res) + "\n")
				} else if strings.Contains(res, "Valid:") {
					b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render("  → " + res) + "\n")
				} else {
					b.WriteString("  → " + res + "\n")
				}
			}
		}
	}

	return b.String()
}

func (m model) resize() model {
	if m.width == 0 || m.height == 0 {
		return m
	}
	m.input.SetWidth(m.width - 4)
	return m
}

// ── info overlay ─────────────────────────────────────────────────────────────

var (
	titleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	infoHeadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	infoDimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

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
	b.WriteString("\n")

	head("Tools")
	row("calls", strconv.Itoa(m.totalToolCalls))
	row("rounds", strconv.Itoa(m.totalToolRounds))
	b.WriteString("\n")

	head("Directory")
	row("working", m.workDir)
	row("log", filepath.Join(m.workDir, "svgpad.log"))
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

// keymap builds the swapping footer bindings for the current state. The
// Edit list is DefaultEditBindings() with {ctrl+n log} spliced before quit
// so the log overlay key is discoverable while typing. The Command list is
// a compact subset of the global commands — the full key reference lives
// in the ? help overlay.
func (m model) keymap() editmode.Keymap {
	def := editmode.DefaultEditBindings()
	edit := make([]editmode.Binding, 0, len(def)+1)
	edit = append(edit, def[:len(def)-1]...)
	edit = append(edit, editmode.Binding{Keys: "ctrl+n", Desc: "log"})
	edit = append(edit, def[len(def)-1])

	cmd := []editmode.Binding{
		{Keys: "e", Desc: "edit"},
		{Keys: "n", Desc: "new"},
		{Keys: "g", Desc: "glyph"},
		{Keys: "t", Desc: "think"},
		{Keys: "r", Desc: "reason:" + strings.TrimSpace(m.thinkModeLabel())},
		{Keys: "y", Desc: "yolo"},
		{Keys: "m", Desc: "info"},
		{Keys: "?", Desc: "help"},
		{Keys: "ctrl+n", Desc: "log"},
		{Keys: "ctrl+c", Desc: "quit"},
	}
	return editmode.Keymap{Edit: edit, Command: cmd}
}

// logPageSize is the per-page scroll distance used by pgup/pgdown in the
// log overlay — matched to the visible content height.
func (m model) logPageSize() int {
	h := m.height - 6
	if h < 1 {
		return 1
	}
	return h
}

// logScrollBy returns a new logTop after moving by delta lines from the
// current view. A scroll up from follow-tail mode anchors the view at the
// current tail; scrolling back to the bottom of the buffer re-enables
// follow mode (-1). The result is clamped to [0, total-innerH].
func (m model) logScrollBy(delta int) int {
	total, innerH := m.logVisibleMetrics()
	maxTop := total - innerH
	if maxTop < 0 {
		maxTop = 0
	}
	top := m.logTop
	if top < 0 { // currently following the tail — anchor here
		top = maxTop
	}
	top += delta
	if top < 0 {
		top = 0
	}
	if top >= maxTop {
		return -1 // back at the tail; resume following
	}
	return top
}

// logVisibleMetrics returns (total wrapped lines, visible content height)
// for the current buffer and window — the two numbers needed for any
// scroll/clamp arithmetic. Computing both in one place keeps the scroll
// math consistent between the dispatcher and the renderer.
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
	wrapped := lipgloss.NewStyle().Width(innerW).Render(strings.Join(lines, "\n"))
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

func (m model) helpOverlay() string {
	var b strings.Builder
	b.WriteString("Keyboard shortcuts\n\n")
	b.WriteString("Command mode (edit box not targeted)\n")
	b.WriteString("  e          edit prompt (target the box)\n")
	b.WriteString("  n          new prompt\n")
	b.WriteString("  g          toggle glyph/image render mode\n")
	b.WriteString("  t          toggle thinking/output box\n")
	b.WriteString("  r          cycle reasoning mode (OFF → HIGH → MAX)\n")
	b.WriteString("  y          toggle YOLO auto-prompt mode\n")
	b.WriteString("  m          show model & metrics info\n")
	b.WriteString("  ? / h      show this help\n\n")
	b.WriteString("Edit mode (edit box targeted)\n")
	b.WriteString("  enter      send prompt\n")
	b.WriteString("  esc        escape to command mode\n")
	b.WriteString("  ctrl+a/e   line start / line end\n")
	b.WriteString("  ctrl+w     delete word back\n\n")
	b.WriteString("Global (work in either mode)\n")
	b.WriteString("  ctrl+n     libds4 log overlay\n")
	b.WriteString("  ctrl+plus  enlarge thinking box\n")
	b.WriteString("  ctrl+minus shrink thinking box\n")
	b.WriteString("  shift+up   shrink thinking box\n")
	b.WriteString("  shift+down enlarge thinking box\n")
	b.WriteString("  shift+left shrink think panel\n")
	b.WriteString("  shift+right enlarge think panel\n")
	b.WriteString("  tab        cycle focus (input → think → tools)\n")
	b.WriteString("  up/down    scroll focused panel\n")
	b.WriteString("  pgup       previous SVG in session\n")
	b.WriteString("  pgdown     next SVG in session\n")
	b.WriteString("  ctrl+q     quit\n")
	b.WriteString("  ctrl+c     quit\n")
	b.WriteString("\n" + infoDimStyle.Render("  press any key to close"))

	box := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		MaxWidth(m.width).
		MaxHeight(m.height).
		Render(b.String()), "svgpad · help")

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// titledBox splices a title into the top border of a lipgloss-rendered box,
// e.g. ╭───────╮ becomes ╭─ Title ─╮. The box must have a plain (uncolored)
// border so the top line is plain runes. Box dimensions are preserved.
func titledBox(rendered, title string) string {
	if title != "" {
		title = " " + title + " "
	}
	lines := strings.SplitN(rendered, "\n", 2)
	top := []rune(lines[0])
	label := []rune(title)
	if len(top) < len(label)+4 { // need ╭─ <label> ─╮
		return rendered
	}
	newTop := string(top[:2]) + titleStyle.Render(string(label)) + string(top[2+len(label):])
	if len(lines) == 2 {
		return newTop + "\n" + lines[1]
	}
	return newTop
}

// wrapAndTruncate pre-wraps content to the given width using lipgloss,
// applies a scroll offset, then truncates/pads to exactly maxLines.
func wrapAndTruncate(content string, width, maxLines, scroll int) string {
	wrapped := lipgloss.NewStyle().Width(width).Render(content)
	lines := strings.Split(wrapped, "\n")
	if scroll > len(lines)-maxLines && len(lines) > maxLines {
		scroll = len(lines) - maxLines
	}
	if scroll < 0 {
		scroll = 0
	}
	start := scroll
	if start > len(lines) {
		start = len(lines)
	}
	end := start + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	lines = lines[start:end]
	for len(lines) < maxLines {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func truncateJSON(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return d.Round(time.Second).String()
}
