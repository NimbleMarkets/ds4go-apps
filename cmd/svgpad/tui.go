package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/dsml"

	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/NimbleMarkets/ds4go-apps/internal/editmode"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/headerbar"
	svg "github.com/NimbleMarkets/ntcharts-svg/svg"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
	"github.com/charmbracelet/x/ansi"
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

const placeholderSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 400">
  <rect width="100%" height="100%" fill="#181825"/>
  <circle cx="200" cy="180" r="50" fill="none" stroke="#cba6f7" stroke-width="4" stroke-dasharray="8 4"/>
  <path d="M 180 180 L 220 180 M 200 160 L 200 200" stroke="#cba6f7" stroke-width="4" stroke-linecap="round"/>
  <text x="200" y="270" fill="#cdd6f4" font-family="sans-serif" font-size="18" font-weight="bold" text-anchor="middle">SVG Playground</text>
  <text x="200" y="300" fill="#a6adc8" font-family="sans-serif" font-size="13" text-anchor="middle">Press 'e' to edit the prompt below</text>
  <text x="200" y="320" fill="#a6adc8" font-family="sans-serif" font-size="13" text-anchor="middle">Press 'Enter' to generate</text>
</svg>`

// ── message types ────────────────────────────────────────────────────────────

type submitMsg struct{ text string }
type yoloSubmitMsg struct{ text string }
type loadEntryMsg struct{}

// Turn-runner messages: the runTurn goroutine forwards GenerationDriver
// events over the bubble.Start channel as these msgs. turnDoneMsg is the
// terminal message of a run.
type roundStartedMsg struct{ round int }
type toolCallsMsg struct{ calls []ds4.ToolCall }
type toolResultsMsg struct{ results []ds4.ChatMessage }
type malformedRetryMsg struct{ reason string }
type turnDoneMsg struct {
	result bubble.RunResult
	err    error
	ctxPos int
}

// engineReadyMsg is delivered from the goroutine that opens the ds4 engine
// and session. The model holds nil engine/session until this lands.
type engineReadyMsg engineinit.Result

// engineReleasedMsg is delivered after a releaseEngineCmd finishes
// closing the session and engine.
type engineReleasedMsg struct{}

// kittyAutoToggleMsg triggers a one-time check to enable Kitty graphics
// when the terminal probe has completed. Fired once from Init, after the
// probe's 250ms timeout window, so a later manual 'g' toggle sticks.
type kittyAutoToggleMsg struct{}

// cachedWidget wraps the rasterized image, document renderer, and document info
// to allow instant rendering and vector-sharp zooming.
type cachedWidget struct {
	img      image.Image
	renderer svg.Renderer
	doc      *svg.Document
}

// widgetCache is an LRU cache of rasterized SVG bitmaps and their renderers,
// keyed by filename. Caching the bitmap and renderer lets a revisited entry
// render instantly while preserving the ability to perform vector-sharp zooming.
type widgetCache struct {
	maxSize int
	keys    []string // least-recently-used first
	entries map[string]cachedWidget
}

func newWidgetCache(maxSize int) *widgetCache {
	return &widgetCache{
		maxSize: maxSize,
		entries: make(map[string]cachedWidget),
	}
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

// Get returns the cached widget for key, marking it most-recently-used.
func (c *widgetCache) Get(key string) (cachedWidget, bool) {
	entry, ok := c.entries[key]
	if !ok {
		return cachedWidget{}, false
	}
	c.touch(key)
	return entry, true
}

// Put stores a widget under key, evicting the least-recently-used entry
// when a new key would push the cache over capacity.
func (c *widgetCache) Put(key string, entry cachedWidget) {
	if _, exists := c.entries[key]; !exists && len(c.keys) >= c.maxSize {
		oldest := c.keys[0]
		c.keys = c.keys[1:]
		if old, ok := c.entries[oldest]; ok && old.renderer != nil {
			_ = old.renderer.Close()
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = entry
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
	think       string
	title       string // from the enrichment pass (or pre-existing on-disk metadata)
	desc        string
	keywords    string
	model       string        // model name stamped into <ai:model> at save time
	generatedAt time.Time     // matches <ai:generatedAt> in the saved file
	genTime     time.Duration // matches <ai:genTime> in the saved file
	svgData     []byte
	toolCalls   []toolCallEntry
}

func spinnerTick() tea.Cmd {
	return bubble.SpinnerTick(200 * time.Millisecond)
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

// releaseEngineCmd closes the supplied session and engine off the TUI
// goroutine. Caller MUST null out m.engine / m.session before dispatch
// so neither is used while the close is racing.
func releaseEngineCmd(eng *ds4.Engine, sess *ds4.Session) tea.Cmd {
	return func() tea.Msg {
		if sess != nil {
			sess.Close()
		}
		if eng != nil {
			eng.Close()
		}
		return engineReleasedMsg{}
	}
}

// ── model ────────────────────────────────────────────────────────────────────

type panelFocus int

const (
	focusInput panelFocus = iota
	focusSVG
	focusThinking
	focusTools
)

type model struct {
	width, height int

	engine        *ds4.Engine // nil until engineReadyMsg
	session       *ds4.Session
	lib           *ds4.Library      // resolved in main, used by Init's goroutine
	engOpts       ds4.EngineOptions // captured to fire async open
	lifecycle     engineLifecycle   // engine state machine, drives badge + transitions
	engineErr     error             // set when StatusError
	modelPath     string
	modelInfo     *ds4.ModelInfo // catalog identity behind modelPath; nil when not catalog-managed
	mtpPath       string
	hasMTP        bool
	mtpDraft      int
	backend       string
	workDir       string
	showInfo      bool
	showImageInfo bool
	showLog       bool           // libds4 log overlay is up (ctrl+n)
	logBuf        *ds4log.Buffer // captured libds4 diagnostics
	logTop        int            // absolute first-visible line; -1 = follow tail

	history          []ds4.ChatMessage
	gen              *bubble.Generation // in-flight generation; nil when idle
	generating       bool
	metadataCtx      context.Context
	metadataCancel   context.CancelFunc
	metadataWG       *sync.WaitGroup
	metadataInFlight bool // tracked between enrichMetadataCmd dispatch and metadataDoneMsg
	statusText       string
	errText          string

	svgWidget svg.Model

	input textinput.Model

	showThinking    bool
	thinkBoxH       int
	thinkBoxW       int // width of the thinking panel (divider position)
	thinkText       string
	thinkMode       ds4.ThinkMode
	thinkScroll     int
	thinkAutoScroll bool // when true, scroll follows new content

	showHelp bool

	panelFocus   panelFocus
	toolScroll   int
	spinnerFrame int

	entries         []svgEntry
	entryIndex      int
	yoloMode        bool
	preserveContext bool // when false, m.history is cleared before each new generation
	yoloCount       int
	outputText      string
	cache           *widgetCache

	tools         *ds4.ToolRegistry
	toolRounds    int
	maxToolRounds int
	toolCalls     []toolCallEntry

	// Streaming-turn state, reset per round. liveCalls maps the decoder's
	// per-block tool-call Index to panel entries; roundCallStart marks where
	// this round's entries begin in toolCalls; pendingCalls is the round's
	// executing batch (from toolCallsMsg).
	liveCalls      []int
	roundCallStart int
	pendingCalls   []ds4.ToolCall

	// Live preview state: previewBase is the draft content at the last
	// round boundary; previewPending holds validated svg_append chunks
	// streamed since then; previewTick throttles re-rasterization.
	previewBase    string
	previewPending []string
	previewTick    int

	totalToolCalls  int
	totalToolRounds int
	toolCallCounts  map[string]int // per-tool cumulative call count

	autoCorrectCount int
	maxAutoCorrect   int
	lastErr          error

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
			think:       md.think,
			model:       md.model,
			generatedAt: md.generatedAt,
			genTime:     md.genTime,
			toolCalls:   md.toolCalls,
		})
	}
	logger.Printf("[SCAN] loaded %d entries", len(result))
	return result
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
	wd, _ := os.Getwd()
	existing := scanExistingSVGs(wd, logger)

	ti := textinput.New()
	ti.Placeholder = defaultPrompt
	initialPrompt := defaultPrompt
	entryIdx := -1
	for i, e := range existing {
		if validateSVG(e.svgData) == "valid" {
			entryIdx = i
			initialPrompt = e.prompt
			break
		}
	}
	ti.SetValue(initialPrompt)

	draftPath := filepath.Join(wd, "draft.svg")
	reg := ds4.NewToolRegistry()

	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "svg_clear",
		Description: "Clear the draft SVG file. Only valid while the draft is empty; once the draft has content, clearing is refused — fix it with svg_replace or svg_replace_lines instead.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		// Guard against the mid-correction death spiral: a model that
		// clears a flawed draft instead of editing it usually never
		// rebuilds, and the turn ends with no SVG at all.
		if data, err := os.ReadFile(draftPath); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			lines := strings.Count(string(data), "\n") + 1
			return fmt.Sprintf("Error: the draft already has content (%d lines); clearing is disabled mid-job. Fix specific issues with svg_replace or svg_replace_lines (you may replace all %d lines in one svg_replace_lines call).", lines, lines), nil
		}
		err := os.WriteFile(draftPath, nil, 0644)
		if err != nil {
			return fmt.Sprintf("Error clearing draft: %v", err), nil
		}
		return "Draft cleared.", nil
	})

	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "svg_read",
		Description: `Read the draft SVG file with line numbers ("NNN | content"). Optional start_line/end_line (1-indexed, inclusive) read just a range. The "NNN | " prefix is display-only and NOT part of the file: never include it in svg_replace targets or replacement text.`,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"start_line":{"type":"integer","description":"First line to read (1-indexed, optional; defaults to 1)"},"end_line":{"type":"integer","description":"Last line to read (inclusive, optional; defaults to end of file)"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		var params struct {
			StartLine int `json:"start_line"`
			EndLine   int `json:"end_line"`
		}
		if len(args) > 0 {
			// Tolerate absent/partial arguments; the zero values mean
			// "whole file" via numberedLines clamping.
			_ = json.Unmarshal(args, &params)
		}
		data, err := os.ReadFile(draftPath)
		if err != nil {
			if os.IsNotExist(err) {
				return "Error: Draft file does not exist. Call svg_clear first.", nil
			}
			return fmt.Sprintf("Error reading draft: %v", err), nil
		}
		if len(data) == 0 {
			return "Draft file is empty.", nil
		}
		return numberedLines(string(data), params.StartLine, params.EndLine), nil
	})

	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "svg_append",
		Description: "Append markup to the end of the draft SVG file.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"chunk":{"type":"string","description":"Markup to append"}},"required":["chunk"]}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		var params struct {
			Chunk string `json:"chunk"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return "", err
		}
		f, err := os.OpenFile(draftPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Sprintf("Error opening draft file: %v", err), nil
		}
		defer f.Close()
		if _, err := f.WriteString(params.Chunk); err != nil {
			return fmt.Sprintf("Error appending to draft: %v", err), nil
		}
		return "Chunk appended successfully.", nil
	})

	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "svg_replace",
		Description: "Replace occurrences of a target substring in the draft SVG file with a replacement string. Useful for targeted corrections.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"The exact substring to replace"},"replacement":{"type":"string","description":"The replacement string"}},"required":["target","replacement"]}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		var params struct {
			Target      string `json:"target"`
			Replacement string `json:"replacement"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return "", err
		}
		data, err := os.ReadFile(draftPath)
		if err != nil {
			return fmt.Sprintf("Error reading draft: %v", err), nil
		}
		content := string(data)
		if !strings.Contains(content, params.Target) {
			return "Error: target substring not found in draft file.", nil
		}
		newContent := strings.Replace(content, params.Target, params.Replacement, 1)
		err = os.WriteFile(draftPath, []byte(newContent), 0644)
		if err != nil {
			return fmt.Sprintf("Error writing draft: %v", err), nil
		}
		return "Target substring replaced successfully.", nil
	})

	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "svg_replace_lines",
		Description: "Replace a range of lines (1-indexed, inclusive) in the draft SVG file with the specified replacement lines.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"start_line":{"type":"integer","description":"The 1-indexed starting line number (inclusive)"},"end_line":{"type":"integer","description":"The 1-indexed ending line number (inclusive)"},"replacement":{"type":"string","description":"The replacement lines"}},"required":["start_line","end_line","replacement"]}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		var params struct {
			StartLine   int    `json:"start_line"`
			EndLine     int    `json:"end_line"`
			Replacement string `json:"replacement"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return "", err
		}
		data, err := os.ReadFile(draftPath)
		if err != nil {
			return fmt.Sprintf("Error reading draft: %v", err), nil
		}
		lines := strings.Split(string(data), "\n")
		if params.StartLine < 1 || params.EndLine < 1 || params.StartLine > len(lines) || params.EndLine > len(lines) || params.StartLine > params.EndLine {
			return fmt.Sprintf("Error: line range %d-%d is invalid. Total lines in file: %d.", params.StartLine, params.EndLine, len(lines)), nil
		}
		var newLines []string
		newLines = append(newLines, lines[0:params.StartLine-1]...)
		newLines = append(newLines, params.Replacement)
		newLines = append(newLines, lines[params.EndLine:]...)
		newContent := strings.Join(newLines, "\n")
		err = os.WriteFile(draftPath, []byte(newContent), 0644)
		if err != nil {
			return fmt.Sprintf("Error writing draft: %v", err), nil
		}
		return "Lines replaced successfully.", nil
	})

	reg.RegisterFunc(ds4.ToolSchema{
		Name:        "svg_validate",
		Description: "Validate the current draft SVG file and report any XML or structural issues.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		data, err := os.ReadFile(draftPath)
		if err != nil {
			if os.IsNotExist(err) {
				return "Error: Draft file is empty or does not exist. Call svg_clear and svg_append first.", nil
			}
			return fmt.Sprintf("Error reading draft: %v", err), nil
		}
		if len(data) == 0 {
			return "Error: Draft file is empty.", nil
		}
		return validateSVGDetailed(string(data)), nil
	})

	cellW, cellH := 8, 16 // default fallback
	if w, h, err := getTerminalCellSize(); err == nil {
		cellW, cellH = w, h
		logger.Printf("[INIT] queried terminal cell size: %dx%d", cellW, cellH)
	} else {
		logger.Printf("[INIT] failed to query terminal cell size: %v (falling back to %dx%d)", err, cellW, cellH)
	}

	svgWidget := svg.NewWithConfig(svg.Config{
		Cols:       80,
		Rows:       24,
		RenderEdge: svgRenderEdge,
		PictureConfig: picture.Config{
			CellPixelWidth:  cellW,
			CellPixelHeight: cellH,
		},
	})
	km := svgWidget.KeyMap()
	km.ToggleMode = key.Binding{}   // Let svgpad handle 't' and 'm'
	km.Reload = key.Binding{}       // Let svgpad handle 'r'
	km.ToggleRender = key.Binding{} // Let svgpad handle 'g'

	metaCtx, metaCancel := context.WithCancel(context.Background())

	return model{
		lib:            lib,
		engOpts:        engOpts,
		lifecycle:      engineLifecycle{status: engineinit.StatusDormant},
		modelPath:      modelPath,
		modelInfo:      app.ModelInfo,
		mtpPath:        mtpPath,
		backend:        backend,
		workDir:        wd,
		input:          ti,
		statusText:     "Ready · viewer mode (press Enter to load engine)",
		ctxSize:        ctxSize,
		logger:         logger,
		debug:          debug,
		logBuf:         logBuf,
		logTop:         -1, // follow the tail by default
		svgWidget:      svgWidget,
		showThinking:   true,
		showImageInfo:  true,
		thinkBoxH:      defaultThink,
		thinkBoxW:      0, // set to 75% of width on first render
		thinkMode:      ds4.ThinkNone,
		cache:          newWidgetCache(50),
		tools:          reg,
		maxToolRounds:  10,
		maxAutoCorrect: 3, // automatically trigger correction turns via svg_validate tool calls
		toolCallCounts: make(map[string]int),
		panelFocus:     focusThinking,
		entries:        existing,
		entryIndex:     entryIdx,
		metadataCtx:    metaCtx,
		metadataCancel: metaCancel,
		metadataWG:     &sync.WaitGroup{},
	}
}

// ── BubbleTea interface ───────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	// The engine is opened lazily on first prompt submit. Startup is
	// Dormant: the viewer panel and entry navigation work without any
	// model loaded.
	var cmds []tea.Cmd
	cmds = append(cmds, m.svgWidget.Init())
	cmds = append(cmds, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
		return kittyAutoToggleMsg{}
	}))
	if m.entryIndex < 0 {
		cmds = append(cmds, m.svgWidget.SetSVGData("placeholder.svg", []byte(placeholderSVG)))
	}
	return tea.Batch(cmds...)
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

	case kittyAutoToggleMsg:
		if cmd := m.autoEnableKittyCmd(); cmd != nil {
			m.logger.Printf("[KITTY] auto-enabling Kitty graphics (probe confirmed support)")
			cmds = append(cmds, cmd)
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
			if m.generating && m.gen != nil {
				m.gen.Cancel()
				m.statusText = "Aborting..."
				return m, nil
			}
			if m.input.Focused() {
				m.input.Blur()
				m.panelFocus = focusThinking
			}
			return m, nil

		case "enter":
			if !m.input.Focused() || m.generating {
				return m, tea.Batch(cmds...)
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				text = defaultPrompt
			}

			m.errText = ""
			var action engineAction
			m.lifecycle, action = m.lifecycle.onSubmit()
			switch action {
			case actionGenerate:
				// Engine ready — start the turn as before.
				m.input.Blur()
				m.panelFocus = focusThinking
				m.yoloCount = 0
				cmds = append(cmds, func() tea.Msg { return submitMsg{text} })
			case actionOpen:
				// Dormant or Error — open the engine now; the
				// engineReadyMsg handler will start generation when ready.
				// Leave m.input alone so the user can still edit/abandon.
				m.statusText = "opening engine…"
				cmds = append(cmds, openEngineCmd(m.lib, m.engOpts, m.ctxSize))
			case actionNone:
				// Already Opening — submit is queued.
				m.statusText = "opening engine… (will submit when ready)"
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
					m.panelFocus = focusSVG
					m.input.Blur()
				case focusSVG:
					m.panelFocus = focusThinking
				case focusThinking:
					m.panelFocus = focusTools
				case focusTools:
					m.panelFocus = focusInput
					cmds = append(cmds, m.input.Focus())
				}
			} else {
				switch m.panelFocus {
				case focusInput:
					m.panelFocus = focusSVG
					m.input.Blur()
				case focusSVG:
					m.panelFocus = focusInput
					cmds = append(cmds, m.input.Focus())
				default:
					m.panelFocus = focusInput
					cmds = append(cmds, m.input.Focus())
				}
			}
			return m, tea.Batch(cmds...)

		case "shift+tab":
			if m.showThinking {
				switch m.panelFocus {
				case focusInput:
					m.panelFocus = focusTools
					m.input.Blur()
				case focusTools:
					m.panelFocus = focusThinking
				case focusThinking:
					m.panelFocus = focusSVG
				case focusSVG:
					m.panelFocus = focusInput
					cmds = append(cmds, m.input.Focus())
				}
			} else {
				switch m.panelFocus {
				case focusInput:
					m.panelFocus = focusSVG
					m.input.Blur()
				case focusSVG:
					m.panelFocus = focusInput
					cmds = append(cmds, m.input.Focus())
				default:
					m.panelFocus = focusInput
					cmds = append(cmds, m.input.Focus())
				}
			}
			return m, tea.Batch(cmds...)

		case "pgup", "k":
			if !m.input.Focused() {
				if len(m.entries) > 0 && m.entryIndex > 0 {
					m.entryIndex--
					if cmd := m.loadEntryCmd(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
				return m, tea.Batch(cmds...)
			}

		case "pgdown", "j":
			if !m.input.Focused() {
				if len(m.entries) > 0 && m.entryIndex < len(m.entries)-1 {
					m.entryIndex++
					if cmd := m.loadEntryCmd(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
				return m, tea.Batch(cmds...)
			}

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
			case focusSVG:
				var cmd tea.Cmd
				m.svgWidget, cmd = m.svgWidget.Update(msg)
				cmds = append(cmds, cmd)
				return m, tea.Batch(cmds...)
			}

		case "down":
			switch m.panelFocus {
			case focusThinking:
				m.thinkScroll++
				return m, nil
			case focusTools:
				m.toolScroll++
				return m, nil
			case focusSVG:
				var cmd tea.Cmd
				m.svgWidget, cmd = m.svgWidget.Update(msg)
				cmds = append(cmds, cmd)
				return m, tea.Batch(cmds...)
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
			m.outputText = ""
			m.history = nil
			m.lastErr = nil
			m.errText = ""
			m.panelFocus = focusInput
			m.entryIndex = -1
			cmds = append(cmds, m.input.Focus())
			cmds = append(cmds, m.svgWidget.SetSVGData("placeholder.svg", []byte(placeholderSVG)))

		case "c":
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
				// The stream-driven turn cannot resume raw logits; rerun the
				// turn after trimming the truncated assistant tail.
				if len(m.history) > 0 && m.history[len(m.history)-1].Role == "assistant" {
					m.history = m.history[:len(m.history)-1]
				}
				m.generating = true
				m.statusText = fmt.Sprintf("Continue [%d/%d]...", m.toolRounds, m.maxToolRounds)
				m.errText = ""
				m.spinnerFrame = 0
				m.genStart = time.Now()
				m.firstTokenTime = time.Time{}
				m.genEnd = time.Time{}
				m.tokenCount = 0
				cmds = append(cmds, spinnerTick())
				var waitCmd tea.Cmd
				m.gen, waitCmd = bubble.Start(m.runTurn)
				cmds = append(cmds, waitCmd)
			} else {
				var svgData []byte
				if m.entryIndex >= 0 && m.entryIndex < len(m.entries) {
					svgData = m.entries[m.entryIndex].svgData
				}
				var v string
				if len(svgData) == 0 {
					v = "no SVG markup found — your response must start with <svg and end with </svg>"
				} else {
					v = validateSVG(svgData)
				}
				if v != "valid" {
					m.generating = true
					m.statusText = "Correcting SVG · " + v
					m.errText = ""
					m.thinkAutoScroll = true
					m.spinnerFrame = 0
					m.genStart = time.Now()
					m.firstTokenTime = time.Time{}
					m.genEnd = time.Time{}
					m.tokenCount = 0
					m.thinkText = ""
					m.outputText = ""
					m.toolCalls = m.toolCalls[:0]
					m.liveCalls = nil
					m.roundCallStart = 0
					m.pendingCalls = nil
					m.previewBase = ""
					m.previewPending = nil
					m.previewTick = 0
					feedback := fmt.Sprintf("Your output was invalid: %s. Please output ONLY a corrected, complete SVG.", v)
					m.history = append(m.history, ds4.ChatMessage{Role: "user", Content: feedback})
					cmds = append(cmds, spinnerTick())
					var waitCmd tea.Cmd
					m.gen, waitCmd = bubble.Start(m.runTurn)
					cmds = append(cmds, waitCmd)
				}
			}
			return m, tea.Batch(cmds...)

		case "m":
			m.showInfo = true

		case "?", "h":
			m.showHelp = true

		case "t":
			m.showThinking = !m.showThinking
			if !m.showThinking && (m.panelFocus == focusThinking || m.panelFocus == focusTools) {
				m.panelFocus = focusSVG
			}
			cmds = append(cmds, m.svgWidget.SetSize(m.svgWidth(), m.svgHeight()))

		case "i":
			m.showImageInfo = !m.showImageInfo
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

		case "x":
			busy := m.generating || m.metadataInFlight
			var action engineAction
			m.lifecycle, action = m.lifecycle.onReleaseRequest(busy)
			if action == actionRelease {
				eng, sess := m.engine, m.session
				m.engine, m.session = nil, nil
				m.statusText = "Engine released"
				return m, releaseEngineCmd(eng, sess)
			}
			if m.lifecycle.releaseRequested {
				if !strings.Contains(m.statusText, "releasing") {
					m.statusText += " · releasing"
				}
			}
			return m, nil

		case "y":
			m.yoloMode = !m.yoloMode
			m.yoloCount = 0
			if m.yoloMode {
				m.statusText = "YOLO mode ON"
			} else {
				m.statusText = "YOLO mode OFF"
			}
			m.logger.Printf("[YOLO] mode=%v", m.yoloMode)

		case "p":
			m.preserveContext = !m.preserveContext
			if m.preserveContext {
				m.statusText = "Context: preserved across generations"
			} else {
				m.statusText = "Context: cleared per generation"
			}
			m.logger.Printf("[CONTEXT] preserve=%v", m.preserveContext)

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

		default:
			if m.panelFocus == focusSVG {
				var cmd tea.Cmd
				m.svgWidget, cmd = m.svgWidget.Update(msg)
				cmds = append(cmds, cmd)
			}
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
				if msg.genTime > 0 {
					m.entries[i].genTime = msg.genTime
				}
				break
			}
		}
		if m.entryIndex >= 0 && m.entryIndex < len(m.entries) &&
			m.entries[m.entryIndex].filename == base {
			m.outputText = entryText(m.entries[m.entryIndex])
		}
		m.metadataInFlight = false
		var action engineAction
		m.lifecycle, action = m.lifecycle.onWorkDone(m.generating)
		if action == actionRelease {
			eng, sess := m.engine, m.session
			m.engine, m.session = nil, nil
			m.statusText = "Engine released"
			return m, releaseEngineCmd(eng, sess)
		}
		return m, nil

	case engineReadyMsg:
		if msg.Err != nil {
			m.lifecycle, _ = m.lifecycle.onEngineOpened(false)
			m.engineErr = msg.Err
			m.statusText = "Error"
			m.errText = msg.Err.Error()
			m.logger.Printf("[ENGINE] open failed: %v", msg.Err)
			return m, nil
		}
		// On success the engine/session are owned by us until release.
		m.engine = msg.Engine
		m.session = msg.Session
		m.hasMTP = msg.HasMTP
		m.mtpDraft = msg.MTPDraft
		m.ctxSize = msg.Session.Ctx()
		var action engineAction
		m.lifecycle, action = m.lifecycle.onEngineOpened(true)
		m.statusText = "Ready"
		m.logger.Printf("[ENGINE] ready  mtp=%v mtpDraft=%d ctx=%d",
			m.hasMTP, m.mtpDraft, m.ctxSize)

		switch action {
		case actionGenerate:
			// Queued submit — fire it now using whatever's currently in
			// the input box (the user could have edited it during open).
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				text = defaultPrompt
			}
			m.input.Blur()
			m.panelFocus = focusThinking
			m.yoloCount = 0
			return m, func() tea.Msg { return submitMsg{text} }
		case actionRelease:
			// Queued release — give the lock back immediately.
			eng, sess := m.engine, m.session
			m.engine, m.session = nil, nil
			m.statusText = "Engine released"
			return m, releaseEngineCmd(eng, sess)
		}
		return m, nil

	case engineReleasedMsg:
		m.lifecycle = m.lifecycle.onEngineReleased()
		m.statusText = "Engine released"
		m.logger.Printf("[ENGINE] released")
		return m, nil

	case submitMsg:
		m.logger.Printf("[USER] %s", msg.text)
		_ = os.Remove(filepath.Join(m.workDir, "draft.svg"))
		if !m.preserveContext {
			m.history = nil
		}
		m.history = append(m.history, ds4.ChatMessage{Role: "user", Content: msg.text})
		m.thinkText = ""
		m.outputText = ""
		m.toolCalls = m.toolCalls[:0]
		m.totalToolCalls = 0
		m.totalToolRounds = 0
		m.toolCallCounts = make(map[string]int)
		m.liveCalls = nil
		m.roundCallStart = 0
		m.pendingCalls = nil
		m.previewBase = ""
		m.previewPending = nil
		m.previewTick = 0
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
		cmds = append(cmds, spinnerTick())
		var waitCmd tea.Cmd
		m.gen, waitCmd = bubble.Start(m.runTurn)
		cmds = append(cmds, waitCmd)

	case bubble.TokenMsg:
		// Metrics cadence only — the panels and preview are fed by
		// StreamEventMsg.
		if m.firstTokenTime.IsZero() {
			m.firstTokenTime = time.Now()
		}
		m.tokenCount++
		cmds = append(cmds, m.gen.Wait())

	case bubble.SpinnerTickMsg:
		if m.generating {
			m.spinnerFrame++
			if !m.genStart.IsZero() {
				round := fmtDuration(time.Since(m.genStart))
				if !m.jobStart.IsZero() && (m.toolRounds > 0 || m.autoCorrectCount > 0) {
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
		// Drop the YOLO tick if the engine was released between when
		// this tick was scheduled and when it fired (e.g., user pressed
		// `x` mid-turn). Re-arming would crash submitMsg with a nil
		// session.
		if m.yoloMode && !m.generating && m.yoloCount < 20 && m.lifecycle.status == engineinit.StatusReady {
			m.history = nil
			cmds = append(cmds, func() tea.Msg { return submitMsg{msg.text} })
		}

	case bubble.StreamEventMsg:
		if cmd := m.applyStreamEvent(msg.Event); cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, m.gen.Wait())

	case roundStartedMsg:
		if msg.round > 0 {
			// Per-round panel/timer reset, mirroring the old ToolRoundMsg
			// handler: jobStart keeps the whole turn's clock.
			m.thinkText = ""
			m.outputText = ""
			m.statusText = m.roundStatus()
			m.errText = ""
			m.thinkAutoScroll = true
			m.genStart = time.Now()
			m.firstTokenTime = time.Time{}
			m.genEnd = time.Time{}
			m.tokenCount = 0
		}
		m.liveCalls = nil
		m.roundCallStart = len(m.toolCalls)
		cmds = append(cmds, m.gen.Wait())

	case toolCallsMsg:
		m.pendingCalls = msg.calls
		var names []string
		for _, call := range msg.calls {
			names = append(names, call.Name)
			m.logger.Printf("[TOOL] arg %s=%q", call.Name, truncateString(call.Arguments, 500))
		}
		m.statusText = fmt.Sprintf("Running %s...", strings.Join(names, ", "))
		m.logger.Printf("[TOOL] round=%d calls=%d", m.toolRounds+1, len(msg.calls))
		cmds = append(cmds, m.gen.Wait())

	case toolResultsMsg:
		m.toolRounds++
		m.totalToolRounds++
		m.totalToolCalls += len(m.pendingCalls)
		// Reconcile panel entries: stream events created entries for
		// decoder-validated blocks; a truncation-repaired block executed
		// without ever streaming, so create what is missing.
		for len(m.toolCalls)-m.roundCallStart < len(m.pendingCalls) {
			call := m.pendingCalls[len(m.toolCalls)-m.roundCallStart]
			m.toolCalls = append(m.toolCalls, toolCallEntry{
				name: call.Name,
				args: truncateJSON(call.Arguments, 120),
			})
		}
		for i, call := range m.pendingCalls {
			m.toolCallCounts[call.Name]++
			var res string
			for _, r := range msg.results {
				if r.ToolCallID == call.ID {
					res = r.Content
					break
				}
			}
			entry := m.roundCallStart + i
			if entry < len(m.toolCalls) {
				m.toolCalls[entry].round = m.toolRounds
				m.toolCalls[entry].result = truncateString(res, 120)
			}
			m.logger.Printf("[TOOL] result id=%s content=%q", call.ID, res)
		}
		m.pendingCalls = nil
		// The draft file is authoritative between rounds: refresh the
		// preview base and push an exact preview.
		m.previewPending = nil
		if data, err := os.ReadFile(filepath.Join(m.workDir, "draft.svg")); err == nil && len(data) > 0 {
			m.previewBase = string(data)
			if inc := extractIncrementalSVG(string(data)); inc != nil {
				cmds = append(cmds, m.svgWidget.SetSVGData("incremental.svg", inc))
			}
		}
		cmds = append(cmds, m.gen.Wait())

	case malformedRetryMsg:
		m.statusText = "Retrying tool syntax..."
		m.logger.Printf("[DSML] malformed tool call: %s", msg.reason)
		cmds = append(cmds, m.gen.Wait())

	case turnDoneMsg:
		m.generating = false
		m.lastErr = msg.err
		if m.gen.Canceled() {
			m.statusText = "Aborted"
		} else {
			m.statusText = "Ready"
		}
		m.gen.Cancel() // release the context's resources
		m.gen = nil
		m.genEnd = time.Now()
		m.ctxPos = msg.ctxPos

		genErr := msg.err
		if errors.Is(genErr, bubble.ErrMaxRounds) {
			// The round budget ran out with calls still pending; the draft
			// may still be salvageable, so fall through to the validation
			// gate like a completed turn.
			m.statusText = "Ready · tool round limit reached"
			genErr = nil
		}
		if genErr != nil && genErr != context.Canceled {
			if errors.Is(genErr, ds4.ErrContextFull) || genErr.Error() == "ds4go: session context full" {
				m.statusText = "Ready · Context full"
				m.errText = "Session context capacity reached. Press 'c' to continue or 'n' for a new prompt."
			} else {
				m.errText = genErr.Error()
				m.statusText = "Error"
			}
		}
		m.logger.Printf("[DONE] %d tok  %.1f tok/s  ttft=%s  gen=%s  ctx=%d/%d  rounds=%d err=%v",
			m.tokenCount, m.decodeSpeed(), fmtDuration(m.ttft()), fmtDuration(m.genTime()),
			m.ctxPos, m.ctxSize, msg.result.ToolRounds, msg.err)

		// Adopt the driver's transcript (it already contains the assistant
		// turns, tool results, and any syntax-error retries).
		if len(msg.result.History) > 0 {
			m.history = msg.result.History
		}
		m.toolRounds = 0

		content := msg.result.Assistant.Content
		var svgData []byte
		var svgFromDraft bool
		if draftData, err := os.ReadFile(filepath.Join(m.workDir, "draft.svg")); err == nil && len(draftData) > 0 {
			svgData = draftData
			svgFromDraft = true
		} else {
			svgData = extractSVG(content)
		}
		outputText := extractOutputText(content, m.thinkMode != ds4.ThinkNone)
		nextPrompt := extractPrompt(content)
		if outputText != "" {
			m.outputText = outputText
		}

		// Determine the prompt for this generation.
		var currentPrompt string
		for i := len(m.history) - 1; i >= 0; i-- {
			if m.history[i].Role == "user" && m.history[i].ToolCallID == "" {
				currentPrompt = m.history[i].Content
				break
			}
		}

		// Auto-correction: if SVG is missing or invalid, feed back error
		// and rerun the whole driver turn (the outer validation gate —
		// app-level semantics on top of the library's DSML recovery).
		if m.autoCorrectCount < m.maxAutoCorrect && msg.err == nil {
			var v string
			if len(svgData) == 0 {
				v = "no SVG markup found in the draft file or in your response"
			} else {
				v = validateSVG(svgData)
			}
			if v != "valid" {
				m.autoCorrectCount++
				m.logger.Printf("[AUTOCORRECT] #%d error=%q", m.autoCorrectCount, v)
				feedback := autoCorrectFeedback(v, svgData, svgFromDraft)
				m.history = append(m.history, ds4.ChatMessage{Role: "user", Content: feedback})
				// Restart: per-segment state reset (the old ToolRoundMsg
				// duties) and a fresh driver run.
				m.generating = true
				m.statusText = m.roundStatus()
				m.errText = ""
				m.thinkText = ""
				m.outputText = ""
				m.thinkAutoScroll = true
				m.genStart = time.Now() // per-segment timer resets, jobStart does not
				m.firstTokenTime = time.Time{}
				m.genEnd = time.Time{}
				m.tokenCount = 0
				m.spinnerFrame = 0
				m.previewPending = nil
				m.liveCalls = nil
				m.pendingCalls = nil
				m.roundCallStart = len(m.toolCalls)
				cmds = append(cmds, spinnerTick())
				var waitCmd tea.Cmd
				m.gen, waitCmd = bubble.Start(m.runTurn)
				cmds = append(cmds, waitCmd)
				return m, tea.Batch(cmds...)
			}
		}
		m.autoCorrectCount = 0

		// Feed the completed SVG to the widget, validate, and auto-save.
		var fname string
		if len(svgData) > 0 {
			cmds = append(cmds, m.svgWidget.SetSVGData("output.svg", svgData))
			v := validateSVG(svgData)
			if genErr == nil && !errors.Is(msg.err, bubble.ErrMaxRounds) {
				if v == "valid" {
					m.statusText = "Ready · SVG valid"
				} else {
					m.statusText = "Ready · " + v
				}
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
				m.metadataInFlight = true
				if m.metadataCancel != nil {
					m.metadataCancel()
				}
				m.metadataCtx, m.metadataCancel = context.WithCancel(context.Background())
				cmds = append(cmds, enrichMetadataCmd(
					m.metadataCtx, m.metadataWG, m.engine, m.modelDisplayName(), path, currentPrompt, svgData, m.genTime(), m.toolCalls, m.thinkText))
			}
		}

		// Record session entry.
		entry := svgEntry{
			filename:  fname,
			prompt:    currentPrompt,
			text:      outputText,
			think:     m.thinkText,
			svgData:   svgData,
			genTime:   m.genTime(),
			toolCalls: make([]toolCallEntry, len(m.toolCalls)),
		}
		copy(entry.toolCalls, m.toolCalls)
		m.entries = append(m.entries, entry)
		m.entryIndex = len(m.entries) - 1
		cmds = append(cmds, m.svgWidget.SetSize(m.svgWidth(), m.svgHeight()))

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

		// Deferred release after a fully-complete turn: if the user
		// pressed `x` mid-turn, fire releaseEngineCmd now that both
		// generation and (any in-flight) metadata are accounted for.
		var releaseAction engineAction
		m.lifecycle, releaseAction = m.lifecycle.onWorkDone(m.metadataInFlight)
		if releaseAction == actionRelease {
			eng, sess := m.engine, m.session
			m.engine, m.session = nil, nil
			m.statusText = "Engine released"
			cmds = append(cmds, releaseEngineCmd(eng, sess))
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

// ── generation ───────────────────────────────────────────────────────────────

// applyStreamEvent folds one dsml.StreamEvent into panel/preview state and
// returns a Cmd when the live preview should re-render. Reasoning and
// content deltas stream live; tool-call events arrive once the enclosing
// block has validated (the decoder buffers them), which with early stop is
// the end of the round — the earliest point a validated chunk exists.
func (m *model) applyStreamEvent(ev dsml.StreamEvent) tea.Cmd {
	switch ev.Type {
	case dsml.EventReasoningDelta:
		m.thinkText += ev.Delta

	case dsml.EventContentDelta:
		m.outputText += ev.Delta
		// Fallback live preview for models that emit bare SVG in text
		// instead of svg_append calls.
		if strings.Contains(m.outputText, "<svg") {
			m.previewTick++
			if strings.Contains(ev.Delta, "\n") || m.previewTick%15 == 0 {
				if inc := extractIncrementalSVG(m.outputText); inc != nil {
					return m.svgWidget.SetSVGData("incremental.svg", inc)
				}
			}
		}

	case dsml.EventToolCallStart:
		m.toolCalls = append(m.toolCalls, toolCallEntry{
			round: m.toolRounds + 1,
			name:  ev.Name,
		})
		m.liveCalls = append(m.liveCalls, len(m.toolCalls)-1)

	case dsml.EventToolCallArgumentsDelta:
		if ev.Index < len(m.liveCalls) {
			entry := m.liveCalls[ev.Index]
			m.toolCalls[entry].args = truncateJSON(m.toolCalls[entry].args+ev.Delta, 120)
		}

	case dsml.EventToolCallEnd:
		if ev.Index >= len(m.liveCalls) {
			break
		}
		entry := m.liveCalls[ev.Index]
		m.toolCalls[entry].args = truncateJSON(ev.Arguments, 120)
		if m.toolCalls[entry].name == "svg_append" {
			if chunk := chunkFromArgs(ev.Arguments); chunk != "" {
				m.previewPending = append(m.previewPending, chunk)
			}
			if inc := previewSVG(m.previewBase, m.previewPending); inc != nil {
				return m.svgWidget.SetSVGData("incremental.svg", inc)
			}
		}
	}
	return nil
}

// runTurn executes one full multi-round tool-calling turn through the
// shared bubble driver. Stream events, round boundaries, and tool batches
// are forwarded as msgs; turnDoneMsg is the terminal message. Tool handlers
// only touch the draft file (each closure captures draftPath alone), so
// executing them off the UI goroutine is safe.
func (m model) runTurn(ctx context.Context, ch chan<- tea.Msg) {
	defer close(ch)

	if m.debug {
		toolsSection, _ := m.tools.RenderToolsSection()
		m.logger.Printf("[PROMPT] tools=%q", toolsSection)
	}

	driver := bubble.NewGenerationDriver(bubble.DriverOptions{
		Engine:    m.engine,
		Session:   m.session,
		Tools:     m.tools,
		ThinkMode: m.thinkMode,
		MaxRounds: m.maxToolRounds + 1, // tool rounds plus the final answer turn
		MaxTokens: 8192,
		ExecuteTools: func(ctx context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
			return m.tools.ExecuteToolCalls(ctx, calls)
		},
		OnEvent: func(e bubble.Event) {
			switch ev := e.(type) {
			case bubble.TokenEvent:
				select {
				case ch <- bubble.TokenMsg(ev.Text):
				default:
					// Drop: TokenMsg only paces metrics; the panels are fed
					// by StreamEventMsg below, which never drops.
				}
			case bubble.StreamEvent:
				ch <- bubble.StreamEventMsg{Event: ev.Event}
			case bubble.RoundStartedEvent:
				ch <- roundStartedMsg{round: ev.Round}
			case bubble.ToolCallsEvent:
				ch <- toolCallsMsg{calls: ev.Calls}
			case bubble.ToolResultsEvent:
				ch <- toolResultsMsg{results: ev.Results}
			case bubble.MalformedRetryEvent:
				ch <- malformedRetryMsg{reason: ev.Reason}
			}
		},
	})

	res, err := driver.RunWithPrompt(ctx, m.systemPrompt(), m.history)
	ch <- turnDoneMsg{result: res, err: err, ctxPos: m.session.Pos()}
}

func (m model) systemPrompt() string {
	base := `You are an SVG artist with access to tools for drafting, editing, and validating SVGs:

  svg_clear() -> "Draft cleared." (refused once the draft has content; edit instead of clearing)
  svg_read(start_line?: int, end_line?: int) -> Draft lines, numbered "NNN | content".
  svg_append(chunk: string) -> "Chunk appended successfully."
  svg_replace(target: string, replacement: string) -> "Target substring replaced successfully."
  svg_replace_lines(start_line: int, end_line: int, replacement: string) -> "Lines replaced successfully."
  svg_validate() -> "Valid: …" on success, "Invalid:\n<diagnostic>" on failure. Checks XML syntax AND renders with the real rasterizer.

Line numbers in svg_read output and svg_validate diagnostics match svg_replace_lines arguments. The "NNN | " prefix is display-only: NEVER include it in svg_append chunks or svg_replace targets/replacements.

Workflow for every user request:

1. At the start of a new request the draft is already empty; construct the SVG by appending chunks using svg_append. (Do NOT call svg_clear if you are correcting, editing, or validating an existing draft — it will be refused.)
2. Validate the drafted SVG by calling svg_validate.
3. If it returns "Invalid:", read the diagnostic — it includes the error line number and a numbered snippet. Use svg_replace or svg_replace_lines to make a MINIMAL fix to just those lines (use svg_read on a small range for more context if needed), then call svg_validate again. Do NOT clear the draft and do NOT rebuild it from scratch.
4. When svg_validate returns "Valid:", emit your final response. You do not need to output the complete SVG in your text response if it has been written to the draft, but confirm completion to the user.

The SVG must include xmlns="http://www.w3.org/2000/svg" and be self-contained.

Tips for successful generation:
- The SVG will be rendered by a static pure-Go parser (no Javascript engine, no HTML5 canvas support, no CSS animations, no external resource loading). You MUST construct the drawing using static SVG elements (like <rect>, <path>, <circle>, <g>, etc.). Do NOT use <script> tags or attempt to draw via Javascript.
- Do NOT use <style> tags containing CSS comments or @import directives (e.g., loading external fonts), as this will cause parser errors in the primitive Go SVG parser. Use standard font-families like "Georgia, serif" or "Impact, sans-serif" directly on elements.
- XML comments (e.g., <!-- comment -->) are welcome, but they should be used sparingly (limitedly), kept concise/non-verbose, always properly closed/terminated, and only included when helpful for context.
- Do NOT use rgba(r, g, b, a) color strings in fill or stroke attributes, as this format is unsupported by the Go color parser. Use hex values or standard rgb(r, g, b) instead, and specify opacity using fill-opacity or stroke-opacity attributes (e.g., fill="rgb(255,200,100)" fill-opacity="0.15").
- Keep your thoughts inside the <think>...</think> block concise, focusing primarily on the visual design, coordinate mapping, and SVG structures. Do NOT output long mathematical derivations or conversational explanations outside the tool calls.
- Always output tool calls using the exact XML syntax: <｜DSML｜invoke name="tool_name"> and </｜DSML｜invoke>. Do not make typos in the tag names (e.g., do not write DSLI, DSigname, or DSML incorrectly).`
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

// extractIncrementalSVG pulls a partial <svg> block from a generating stream
// and appends a closing </svg> tag if it is still open, making it possible
// to render a preview of the SVG as it generates.
func extractIncrementalSVG(s string) []byte {
	start := strings.Index(s, "<svg")
	if start == -1 {
		return nil
	}
	end := strings.Index(s[start:], "</svg>")
	if end == -1 {
		partial := s[start:]
		if !strings.HasSuffix(strings.TrimSpace(partial), "</svg>") {
			partial = partial + "</svg>"
		}
		return []byte(partial)
	}
	return []byte(s[start : start+end+len("</svg>")])
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
// If thinkActive is true, it also strips the thinking block which may start at the
// beginning without a <think> tag.
func extractOutputText(s string, thinkActive bool) string {
	// Strip <think>...</think>
	for {
		start := strings.Index(s, "<think>")
		if start != -1 {
			end := strings.Index(s[start:], "</think>")
			if end == -1 {
				s = strings.TrimSpace(s[:start])
				break
			}
			s = strings.TrimSpace(s[:start] + s[start+end+len("</think>"):])
			thinkActive = false
		} else {
			if thinkActive {
				end := strings.Index(s, "</think>")
				if end != -1 {
					s = strings.TrimSpace(s[end+len("</think>"):])
					// Set thinkActive to false so we don't try to strip again (which would loop infinitely)
					thinkActive = false
					continue
				}
				// If thinkActive is true but </think> is not yet received, then the entire string is thinking,
				// so the output text is empty.
				s = ""
				break
			}
			break
		}
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

// validateSVG and validateSVGDetailed live in validate.go.

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

// autoEnableKittyCmd switches the SVG widget from glyph to Kitty rendering
// when the startup probe confirmed support. No-op if the probe resolved
// Unsupported (or hasn't resolved) or the widget is already in Kitty mode,
// so it never undoes a state the user chose.
func (m *model) autoEnableKittyCmd() tea.Cmd {
	if m.svgWidget.KittySupported() == picture.KittyCapabilitySupported &&
		m.svgWidget.RenderMode() == svg.RenderGlyph {
		return m.svgWidget.ToggleRenderMode()
	}
	return nil
}

// kittyBadge renders the header status pill indicating whether Kitty graphics is supported.
func (m model) kittyBadge() string {
	supported := m.svgWidget.KittySupported()

	var style lipgloss.Style
	var label string

	switch supported {
	case picture.KittyCapabilitySupported:
		style = lipgloss.NewStyle().
			Background(lipgloss.Color("33")). // Vibrant blue for active Kitty graphics
			Foreground(lipgloss.Color("231")).
			Bold(true)
		label = "🐱 kitty"
	case picture.KittyCapabilityUnsupported:
		style = lipgloss.NewStyle().
			Background(lipgloss.Color("240")). // Muted gray for fallback glyph rendering
			Foreground(lipgloss.Color("250")).
			Bold(true)
		label = "🐱 glyph"
	default:
		style = lipgloss.NewStyle().
			Background(lipgloss.Color("214")). // Amber/yellow for checking status
			Foreground(lipgloss.Color("16")).
			Bold(true)
		label = "🐱 query"
	}

	padded := " " + label + " "
	const width = 10
	for lipgloss.Width(padded) < width {
		padded += " "
	}
	return style.Render(padded)
}

// modelDisplayName returns the catalog alias for the loaded model when it
// resolves to a ds4 catalog entry — the ds4flash.gguf default link's
// basename says nothing about the actual model — falling back to the
// file's basename.
func (m model) modelDisplayName() string {
	if m.modelInfo != nil && m.modelInfo.Alias != "" {
		return m.modelInfo.Alias
	}
	return filepath.Base(m.modelPath)
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
	m.thinkText = e.think
	m.outputText = entryText(e)
	m.thinkScroll = 0
	m.toolScroll = 0
	m.toolCalls = make([]toolCallEntry, len(e.toolCalls))
	copy(m.toolCalls, e.toolCalls)
	m.input.SetValue(e.prompt)
	if e.filename != "" {
		if cached, ok := m.cache.Get(e.filename); ok {
			return m.svgWidget.SetImageAndRenderer(cached.img, cached.renderer, cached.doc)
		}
	}
	if len(e.svgData) > 0 {
		return m.svgWidget.SetSVGData(e.filename, e.svgData)
	}
	return nil
}

func (m model) infoWidth() int {
	if !m.showImageInfo {
		return 0
	}
	if m.entryIndex < 0 || m.entryIndex >= len(m.entries) {
		return 0
	}
	infoW := 30
	if mx := m.width / 3; infoW > mx {
		infoW = mx
	}
	if infoW < 18 || m.width-infoW < 40 {
		return 0
	}
	return infoW
}

func (m model) svgWidth() int {
	w := m.width - m.infoWidth() - 2
	if w < 10 {
		w = 10
	}
	return w
}

// svgHeight returns the content height for the SVG widget, accounting for the
// thinking box and error bars when visible.
func (m model) svgHeight() int {
	h := m.height - headerH - footerH - inputH
	if m.showThinking {
		h -= m.thinkBoxH + 2 // + border
	}
	if m.errText != "" {
		h -= 1
	}

	// Compute SVG errors to see if we need an error bar for partial rendering
	var svgErr error
	if !m.generating {
		if err := m.svgWidget.Err(); err != nil {
			svgErr = err
		} else if err := m.svgWidget.RendererErr(); err != nil {
			svgErr = err
		} else if m.entryIndex >= 0 && m.entryIndex < len(m.entries) {
			e := m.entries[m.entryIndex]
			if len(e.svgData) > 0 {
				if v := validateSVG(e.svgData); v != "valid" {
					svgErr = fmt.Errorf("SVG validation failed: %s", v)
				}
			}
		}
	}
	hasPartialRender := false
	if m.svgWidget.Image() != nil && strings.TrimSpace(m.svgWidget.View().Content) != "" {
		hasPartialRender = true
	}
	if svgErr != nil && hasPartialRender {
		h -= 1
	}

	h -= 2 // border around SVG pane
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

	// Compute SVG errors early so we can show them in the header or style the border
	var svgErr error
	if !m.generating {
		if err := m.svgWidget.Err(); err != nil {
			svgErr = err
		} else if err := m.svgWidget.RendererErr(); err != nil {
			svgErr = err
		} else if m.entryIndex >= 0 && m.entryIndex < len(m.entries) {
			e := m.entries[m.entryIndex]
			if len(e.svgData) > 0 {
				if v := validateSVG(e.svgData); v != "valid" {
					svgErr = fmt.Errorf("SVG validation failed: %s", v)
				}
			}
		}
	}

	// Header — plain text to avoid lipgloss v2 truncation issues
	modelName := m.modelDisplayName()
	if lipgloss.Width(modelName) > 20 {
		modelName = modelName[:17] + "..."
	}
	status := m.statusText
	if m.errText != "" {
		cleanErr := strings.ReplaceAll(m.errText, "\n", " | ")
		status = "Error: " + cleanErr
		if len(status) > 40 {
			status = status[:37] + "..."
		}
	} else if svgErr != nil {
		cleanErr := strings.ReplaceAll(svgErr.Error(), "\n", " | ")
		status = "Error: " + cleanErr
		if len(status) > 40 {
			status = status[:37] + "..."
		}
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
		engineinit.Badge(m.lifecycle.status)+" "+m.kittyBadge(),
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
	if m.entryIndex >= 0 && m.entryIndex < len(m.entries) {
		fname := m.entries[m.entryIndex].filename
		if fname != "" {
			if img := m.svgWidget.Image(); img != nil {
				m.cache.Put(fname, cachedWidget{
					img:      img,
					renderer: m.svgWidget.Renderer(),
					doc:      m.svgWidget.Document(),
				})
			}
		}
	}

	// Determine if the widget was able to render partial drawing content.
	hasPartialRender := false
	if m.svgWidget.Image() != nil && strings.TrimSpace(svgPanel) != "" {
		hasPartialRender = true
	}

	var svgContent string
	if svgErr != nil && !hasPartialRender {
		// Format error message nicely
		var b strings.Builder
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true).Render("SVG Render/Parse Error"))
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(svgErr.Error()))
		if m.entryIndex >= 0 && m.entryIndex < len(m.entries) {
			b.WriteString("\n\n")
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(fmt.Sprintf("File: %s", m.entries[m.entryIndex].filename)))
		}
		svgContent = wrapAndTruncate(b.String(), svgW, svgH, 0)
	} else if strings.TrimSpace(svgPanel) == "" || svgPanel == " No SVG yet — press Enter to generate" {
		svgContent = wrapAndTruncate(" No SVG yet — press Enter to generate", svgW, svgH, 0)
	} else {
		// Normal drawing containing Kitty graphics or block art
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
				svgLines[i] = ansi.Truncate(svgLines[i], svgW, "")
			}
		}
		svgContent = strings.Join(svgLines, "\n")
	}

	borderStyle := lipgloss.NewStyle().Border(lipgloss.NormalBorder())
	if svgErr != nil {
		borderStyle = borderStyle.BorderForeground(lipgloss.Color("203")) // Red border on error
	} else if m.panelFocus == focusSVG {
		borderStyle = borderStyle.BorderForeground(lipgloss.Color("99")) // Highlighted mauve/purple when focused
	} else {
		borderStyle = borderStyle.BorderForeground(lipgloss.Color("240")) // Muted when unfocused
	}
	svgContentBox := borderStyle.Width(svgW + 2).Height(svgH + 2).Render(svgContent)

	infoW := m.infoWidth()
	var svgRow string
	if infoW > 0 {
		var rawInfo string
		if m.entryIndex >= 0 && m.entryIndex < len(m.entries) {
			rawInfo = renderEntryInfo(m.entries[m.entryIndex], m.svgWidget.Document(), infoW-2)
		}
		baseBorder := lipgloss.NewStyle().Border(lipgloss.NormalBorder())
		infoPanel := baseBorder.BorderForeground(lipgloss.Color("240")).
			Width(infoW).
			Height(svgH + 2).
			Render(wrapAndTruncate(rawInfo, infoW-2, svgH, 0))
		svgRow = lipgloss.JoinHorizontal(lipgloss.Top, infoPanel, svgContentBox)
	} else {
		svgRow = svgContentBox
	}

	// Thinking / output box + tool calls panel
	var sections []string
	sections = append(sections, header)
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
	if svgErr != nil && hasPartialRender {
		errStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("208")). // Amber/orange background for recoverable SVG errors
			Foreground(lipgloss.Color("255")).
			Bold(true).
			Width(m.width)
		msg := " SVG WARNING: " + strings.ReplaceAll(svgErr.Error(), "\n", " | ")
		if lipgloss.Width(msg) > m.width {
			msg = ansi.Truncate(msg, m.width-3, "...")
		}
		if w := lipgloss.Width(msg); w < m.width {
			msg += strings.Repeat(" ", m.width-w)
		}
		sections = append(sections, errStyle.Render(msg))
	}
	if m.showThinking {
		var thinkContent string
		if m.thinkText != "" && m.outputText != "" {
			thinkContent = m.thinkText + "\n─────────────────\n" + m.outputText
		} else if m.thinkText != "" {
			thinkContent = m.thinkText
		} else if m.outputText != "" {
			thinkContent = m.outputText
		} else {
			if m.thinkMode == ds4.ThinkNone {
				thinkContent = "(reasoning off — r to enable in command mode)"
			} else {
				thinkContent = "…"
			}
		}

		// Two side-by-side panels — reasoning / tools — laid
		// out side-by-side using lipgloss.JoinHorizontal.
		// They share the full width, divided by the resizable m.thinkBoxW.
		remaining := m.width

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

		// Auto-scroll: estimate the wrapped line count against the
		// reasoning cell's inner width (cell width minus its 2-cell
		// border) so the scroll offset follows new content. The exact
		// re-wrap happens inside each cell's content generator.
		wrapped := lipgloss.NewStyle().Width(thinkW - 2).Render(thinkContent)
		bottomScroll := len(strings.Split(wrapped, "\n")) - m.thinkBoxH
		if bottomScroll < 0 {
			bottomScroll = 0
		}
		if m.thinkAutoScroll {
			m.thinkScroll = bottomScroll
		} else if m.thinkScroll >= bottomScroll {
			m.thinkAutoScroll = true
			m.thinkScroll = bottomScroll
		}

		thinkBorderColor := lipgloss.Color("240")
		toolBorderColor := lipgloss.Color("240")
		if m.panelFocus == focusThinking {
			thinkBorderColor = lipgloss.Color("75")
		} else if m.panelFocus == focusTools {
			toolBorderColor = lipgloss.Color("75")
		}
		baseBorder := lipgloss.NewStyle().Border(lipgloss.NormalBorder())

		// Raw (unwrapped) panel bodies; the cell content generators
		// wrap them to whatever inner size FlexBox hands them.
		rawThink := thinkContent
		rawTool := m.renderToolPanel()
		thinkScroll, toolScroll := m.thinkScroll, m.toolScroll

		var panels []string
		thinkPanel := baseBorder.BorderForeground(thinkBorderColor).
			Width(thinkW).
			Height(m.thinkBoxH + 2).
			Render(wrapAndTruncate(rawThink, thinkW-2, m.thinkBoxH, thinkScroll))
		panels = append(panels, thinkPanel)

		toolPanel := baseBorder.BorderForeground(toolBorderColor).
			Width(toolW).
			Height(m.thinkBoxH + 2).
			Render(wrapAndTruncate(rawTool, toolW-2, m.thinkBoxH, toolScroll))
		panels = append(panels, toolPanel)

		sections = append(sections, lipgloss.JoinHorizontal(lipgloss.Top, panels...))
	}
	sections = append(sections, svgRow)

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

	// Tool Calls header
	headStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	b.WriteString(headStyle.Render("Tool Calls") + "\n")

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
				b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("  args: "+tc.args) + "\n")
			}
			if tc.result != "" {
				res := tc.result
				if strings.Contains(res, "Invalid:") {
					b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("  → "+res) + "\n")
				} else if strings.Contains(res, "Valid:") {
					b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render("  → "+res) + "\n")
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
	if info := m.modelInfo; info != nil {
		alias := info.Alias
		if info.Default {
			alias += " (default)"
		}
		row("catalog", alias)
		row("file", info.FileName)
		row("size", fmt.Sprintf("%.0f GB", info.SizeGB))
		sha := info.SHA256
		if len(sha) > 16 {
			sha = sha[:16] + "…"
		}
		row("sha256", sha)
		if info.Notes != "" {
			row("notes", info.Notes)
		}
	} else {
		row("file", filepath.Base(m.modelPath))
	}
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
	ctxMode := "cleared per generation"
	if m.preserveContext {
		ctxMode = "preserved across generations"
	}
	row("context mode", ctxMode)
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
		{Keys: "c", Desc: "continue"},
		{Keys: "g", Desc: "glyph"},
		{Keys: "t", Desc: "think"},
		{Keys: "r", Desc: "reason:" + strings.TrimSpace(m.thinkModeLabel())},
		{Keys: "y", Desc: "yolo"},
		{Keys: "p", Desc: "preserve"},
		{Keys: "i", Desc: "image info"},
		{Keys: "m", Desc: "info"},
		{Keys: "x", Desc: "release"},
		{Keys: "?", Desc: "help"},
		{Keys: "ctrl+n", Desc: "log"},
		{Keys: "< / >", Desc: "power"},
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
	b.WriteString("  c          continue generating or correct SVG\n")
	b.WriteString("  g          toggle glyph/image render mode\n")
	b.WriteString("  t          toggle thinking/output box\n")
	b.WriteString("  r          cycle reasoning mode (OFF → HIGH → MAX)\n")
	b.WriteString("  y          toggle YOLO auto-prompt mode\n")
	b.WriteString("  i          toggle image info pane\n")
	b.WriteString("  < / >      adjust GPU throttle power (-/+ 10%)\n")
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
	b.WriteString("  tab/shift+tab cycle focus (input → SVG → think → tools)\n")
	b.WriteString("  up/down/left/right scroll or pan focused panel\n")
	b.WriteString("  pgup/pgdown or k/j previous/next SVG in session\n")
	b.WriteString("  +/-/0      zoom in / zoom out / reset (when SVG pane is focused)\n")
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
	content = strings.ReplaceAll(content, "\r", "")
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
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "")
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

// roundStatus is the status line shown when a generation round starts.
// Auto-correct retries get their own label: they restart the per-segment
// tool-round counter, so a bare "Generate [0/10]" minutes into a job would
// look like a stuck counter rather than a retry.
func (m model) roundStatus() string {
	if m.autoCorrectCount > 0 {
		return fmt.Sprintf("Fixing SVG (%d/%d) [%d/%d]...",
			m.autoCorrectCount, m.maxAutoCorrect, m.toolRounds, m.maxToolRounds)
	}
	return fmt.Sprintf("Generate [%d/%d]...", m.toolRounds, m.maxToolRounds)
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return d.Round(time.Second).String()
}
