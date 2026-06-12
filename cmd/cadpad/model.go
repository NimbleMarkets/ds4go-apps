// Package main (cadpad) - Bubble Tea TUI model and initialization.
package main

import (
	"context"
	"image"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/harness"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/lua"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/luals"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/tools"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// Messages used by the Bubble Tea update loop.
type engineReadyMsg engineinit.Result

type previewUpdatedMsg struct {
	name string
	img  image.Image
	ok   bool
	err  string
}

type toolDoneMsg struct {
	text      string
	err       error
	reasoning string
}

type statusMsg string

type driverEventMsg struct {
	e bubble.Event
}

type generationStartedMsg struct {
	ch chan tea.Msg
}

// kittyAutoToggleMsg triggers a one-time check to enable Kitty graphics
// when the terminal probe has completed.
type kittyAutoToggleMsg struct{}

// camIdleMsg fires after camIdleDelay following a camera interaction. seq
// identifies which interaction armed it: a tick whose seq no longer matches
// camSeq is stale (the camera moved again) and is dropped.
type camIdleMsg struct{ seq int }

type engineLifecycle struct {
	status engineinit.Status
	err    error
}

// focusRegion identifies which UI region has keyboard focus.
type focusRegion int

const (
	focusViewport focusRegion = iota
	focusThinking
	focusLuaOutput
	focusSource
)

// model is the Bubble Tea root.
type model struct {
	w        *world.World
	renderer *render.Renderer
	proj     render.Projection
	selected int

	// Camera state for the angled (3D) view.
	camAzimuth   float32
	camElevation float32
	camZoom      float32
	camPanX      float32
	camPanY      float32

	// Resolution preset index for preview quality.
	resIndex int // 0=192 1=384 2=768 3=1536

	// Current Lua file for the active generation (restricts tool scope).
	// Pointer so tool closures registered in newModel see updates.
	currentLuaFile *string

	lib       *ds4.Library
	engOpts   ds4.EngineOptions
	engine    *ds4.Engine
	session   *ds4.Session
	tools     *ds4.ToolRegistry
	thinkMode ds4.ThinkMode
	maxRounds int

	pic    picture.Model
	input  textinput.Model
	logBuf *ds4log.Buffer
	logger *log.Logger

	width, height int
	status        string
	lastErr       string
	toolHistory   []string
	showHelp      bool
	showLog       bool
	logTop        int

	showThinking  bool
	showLuaOutput bool

	// Source panel ('v'): highlighted lines of the active lua script,
	// shown beside the LLM Output box. sourceScroll counts lines back
	// from the tail; 0 follows new content as the model writes the file.
	showSource   bool
	sourceName   string
	sourceLines  []string
	sourceScroll int

	focus       focusRegion
	thinkScroll int
	luaScroll   int

	reasoningLog  []string
	lastThinking  string
	lastLuaOutput string

	inferencing  bool
	spinnerFrame int

	lifecycle engineLifecycle
	ctxSize   int
	modelPath string
	backend   string
	debug     bool

	luaWorkspace  string
	luaDiag       *luals.Diagnoser
	lastActiveLua string
	luaEntries    []luaEntry
	luaEntryIndex int

	genCh chan tea.Msg

	generationLog []string
	genRound      int // current round number during generation

	// Mouse tracking
	mouseDragging bool
	lastMouseX    int
	lastMouseY    int

	// sizedOnce is set by the first WindowSizeMsg, which also triggers the
	// initial preview render (Init cannot — the size is unknown there).
	sizedOnce bool

	// Preview render throttling/coalescing
	renderingPreview bool
	previewDirty     bool

	// Progressive refinement for the 3D angle view: camera interactions
	// render at reduced resolution for instant feedback, then a camIdleMsg
	// triggers the full-resolution pass once input has settled.
	previewLowRes bool
	camSeq        int

	// View cache for high-frequency input events
	cachedView *string
}

type luaEntry struct {
	filename string
	path     string
	modTime  time.Time
}

func newModel(app *appinit.App) model {
	// Local aliases so the body below is unchanged from the pre-appinit form.
	lib := app.Lib
	engOpts := app.EngineOpts
	ctxSize := app.Flags.Ctx
	modelPath := app.EngineOpts.ModelPath
	backend := app.Flags.Backend
	logger := app.Logger
	logBuf := app.LogBuf
	debug := app.Flags.Debug
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
		w:         w,
		renderer:  rend,
		proj:      render.ProjAngle,
		lib:       lib,
		engOpts:   engOpts,
		ctxSize:   ctxSize,
		modelPath: modelPath,
		backend:   backend,
		logger:    logger,
		logBuf:    logBuf,
		debug:     debug,
		input:     ti,
		pic:       pic,
		tools:     ds4.NewToolRegistry(),
		thinkMode: ds4.ThinkNone,
		maxRounds: 36,
		lifecycle: engineLifecycle{status: engineinit.StatusDormant},
		status:    "Ready. Type a modeling request or /create box base 4 3 2",
		logTop:    -1,
		// Default orbit matches the old hardcoded corner view.
		camAzimuth:     0.61,
		camElevation:   0.46,
		camZoom:        1.0,
		camPanX:        0,
		camPanY:        0,
		currentLuaFile: new(string),
		focus:          focusViewport,
		cachedView:     new(string),
	}

	if lib == nil {
		m.status = "geometry only (no LLM engine). Slash commands + previews work; natural language needs a real model."
	}

	// Register only non-geometry tools. The LLM must use Lua for all modeling.
	harness.RegisterSelected(m.tools, m.w, m.renderer,
		"cad_describe",
		"cad_bbox",
		"cad_export_stl",
		"cad_save",
		"cad_load",
	)

	workspace := filepath.Join(mustUserHome(), ".cadpad", "lua-workspace")

	// Best-effort lua-language-server diagnostics for generated scripts.
	// If the sdf stub cannot be written the server would flag every sdf.* call
	// as an undefined global — worse than no diagnostics — so skip it entirely.
	defsDir := filepath.Join(mustUserHome(), ".cadpad", "lua-defs")
	if err := luals.WriteDefs(defsDir); err != nil {
		logger.Printf("lua diagnostics disabled: write defs: %v", err)
	} else {
		diag, err := luals.New(context.Background(), workspace, defsDir, luals.DefaultTimeout)
		switch {
		case err != nil:
			logger.Printf("lua diagnostics disabled: %v", err)
		case diag == nil:
			logger.Printf("lua diagnostics disabled: lua-language-server not on PATH")
		default:
			m.luaDiag = diag
			go diag.Warmup(context.Background())
		}
	}

	lft := tools.LuaFileTools{
		Workspace:   workspace,
		W:           m.w,
		R:           m.renderer,
		CurrentFile: m.currentLuaFile,
	}
	if m.luaDiag != nil {
		lft.Diag = m.luaDiag
	}
	if err := tools.RegisterLuaFileTools(m.tools, lft); err != nil {
		logger.Printf("warning: failed to register lua file tools: %v", err)
	}

	m.luaWorkspace = workspace
	m.luaEntries = scanExistingLuaFiles(workspace, logger)
	if len(m.luaEntries) > 0 {
		m.luaEntryIndex = 0
	}

	// The lua source panel is part of the default layout; load the most
	// recent workspace script if one exists, else it shows a placeholder.
	m.showSource = true
	m.reloadSource()

	return m
}

func mustUserHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func scanExistingLuaFiles(dir string, logger *log.Logger) []luaEntry {
	if err := os.MkdirAll(dir, 0755); err != nil {
		if logger != nil {
			logger.Printf("[SCAN] mkdir lua workspace %q: %v", dir, err)
		}
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if logger != nil {
			logger.Printf("[SCAN] read dir %q: %v", dir, err)
		}
		return nil
	}

	var files []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".lua") {
			files = append(files, e)
		}
	}

	if logger != nil {
		logger.Printf("[SCAN] found %d .lua files in %q", len(files), dir)
	}

	sort.Slice(files, func(i, j int) bool {
		ii, _ := files[i].Info()
		ji, _ := files[j].Info()
		return ii.ModTime().After(ji.ModTime())
	})

	var result []luaEntry
	for _, f := range files {
		info, _ := f.Info()
		result = append(result, luaEntry{
			filename: f.Name(),
			path:     filepath.Join(dir, f.Name()),
			modTime:  info.ModTime(),
		})
	}

	if logger != nil {
		logger.Printf("[SCAN] loaded %d lua entries", len(result))
	}
	return result
}

func (m model) Init() tea.Cmd {
	// Sync the startup viewport with the source panel: build the world from
	// the same script the panel shows. The debug box is only a fallback so
	// the preview pane is never empty on a fresh workspace.
	if m.luaEntryIndex >= 0 && m.luaEntryIndex < len(m.luaEntries) {
		entry := m.luaEntries[m.luaEntryIndex]
		st := lua.NewState(m.w, m.renderer)
		if err := st.DoFile(entry.path); err != nil {
			m.logger.Printf("[INIT] startup load %s failed: %v", entry.filename, err)
		}
		st.Close()
	}
	if len(m.w.Names()) == 0 {
		m.w.Create("debug_box", "box", map[string]float64{"x": 4, "y": 3, "z": 2})
	}

	// The initial preview render waits for the first WindowSizeMsg — at
	// Init time the terminal size is unknown and a width-0 layout would
	// produce a tiny raster that later resizes never replace.
	cmds := []tea.Cmd{
		m.pic.Init(),
		picture.RequestCellSize(),
		picture.QueryKittySupport(),
		tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
			return kittyAutoToggleMsg{}
		}),
	}

	if m.lib != nil {
		cmds = append(cmds, func() tea.Msg {
			res := engineinit.Open(m.lib, m.engOpts, m.ctxSize)
			return engineReadyMsg(res)
		})
	}

	return tea.Batch(cmds...)
}
