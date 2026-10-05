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
	"github.com/NimbleMarkets/ds4go-apps/internal/modelpicker"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go-apps/internal/runconfig"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// Messages used by the Bubble Tea update loop.
type engineReadyMsg engineinit.Result

type previewUpdatedMsg struct {
	name  string
	epoch int
	img   image.Image
	ok    bool
	err   string

	// mode reports which renderer produced the frame (GPU vs CPU mesh
	// fallback). It is only meaningful — and hasMode only true — for the live
	// 3D auto path (RenderAngledAuto); the deliberate CPU paths (Render, the
	// one-shot RenderAngledScale) leave hasMode false so the badge is not
	// shown for them. (RenderModeGPU is the zero value, so a plain zero mode
	// must not be treated as "GPU".)
	mode    render.RenderMode
	hasMode bool
	// render is the renderer's wall time for this frame (GPU dispatch and
	// readback, or the CPU path), shown in the viewport header.
	render time.Duration

	// hq marks a frame produced by the high-quality settle render
	// (RenderAngledAutoQ at HighGPUQuality). The settle handler uses it to avoid
	// scheduling another settle tick off an HQ frame (no render loop).
	hq bool
}

// camSettleMsg fires ~150ms after the camera last moved. epoch is the
// renderEpoch captured when the tick was scheduled; if it no longer matches
// m.renderEpoch (a newer render was requested in the meantime) the tick is
// stale and ignored, so a re-render only happens once the view truly settled.
type camSettleMsg struct{ epoch int }

// gpuWarmedUpMsg is delivered after the startup GPU warm-up probe completes; it
// carries no state (the device/pipeline caches live in the render package) and
// exists only so the first real frame doesn't pay synchronous device creation.
type gpuWarmedUpMsg struct{}

type toolDoneMsg struct {
	history   []ds4.ChatMessage
	ctxPos    int
	text      string
	err       error
	reasoning string
}

type statusMsg string

type driverEventMsg struct {
	e bubble.Event
}

// kittyAutoToggleMsg triggers a one-time check to enable Kitty graphics
// when the terminal probe has completed.
type kittyAutoToggleMsg struct{}

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
	controls      padui.Model
	picker        modelpicker.Model
	loading       padui.Loading
	runOptions    runconfig.Options
	pendingSubmit bool
	mtpEnabled    bool
	activeThink   ds4.ThinkMode

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
	history   []ds4.ChatMessage
	ctxPos    int

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

	inferencing   bool
	gen           *bubble.Generation
	quitRequested bool
	forceQuit     bool // second Ctrl+C during engine load; see appinit.ForceExit
	opening       bool
	spinnerFrame  int

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

	// renderEpoch is bumped every time a preview render is REQUESTED (in
	// refreshPreview). A settle tick captures the epoch at schedule time and is
	// only honored if it still matches when it fires — i.e. nothing newer was
	// requested in the 150ms window, so the camera has genuinely settled.
	renderEpoch int

	// hqRender is a one-shot flag: when set, the next angle-view render uses
	// the slow, smooth SDF sphere tracer instead of the fast cached mesh.
	// The 'R' key sets it and immediately resets it so camera moves stay on
	// the fast path.
	hqRender bool

	// lastRenderMode records which renderer (GPU/CPU) produced the most recent
	// live 3D frame; showRenderMode gates the viewport badge so it only appears
	// once a real auto-path frame has reported a mode.
	lastRenderMode render.RenderMode
	// Frame cost telemetry for the viewport header: raster on screen, render
	// time, and the picture widget's encode time and payload size.
	lastRaster      image.Rectangle
	lastRender      time.Duration
	lastEncode      time.Duration
	lastEncodeBytes int
	transport       padui.KittyTransport // requested
	lastTransport   padui.KittyTransport // used by the last frame, after any fallback
	showRenderMode  bool

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
		w:          w,
		renderer:   rend,
		proj:       render.ProjAngle,
		lib:        lib,
		mtpEnabled: app.Flags.MTP != "none",
		runOptions: runconfig.Options{Temperature: .7, TopP: .95, ToolRounds: 36},
		engOpts:    engOpts,
		ctxSize:    ctxSize,
		modelPath:  modelPath,
		backend:    backend,
		opening:    false,
		logger:     logger,
		logBuf:     logBuf,
		debug:      debug,
		input:      ti,
		pic:        pic,
		tools:      ds4.NewToolRegistry(),
		thinkMode:  ds4.ThinkNone,
		maxRounds:  36,
		lifecycle:  engineLifecycle{status: engineinit.StatusDormant},
		status:     "Ready. Type a modeling request or /create box base 4 3 2",
		logTop:     -1,
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
		m.warmUpGPUCmd(),
	}

	if m.lib != nil && m.modelPath == "" {
		cmds = append(cmds, func() tea.Msg { return chooseModelMsg{} })
	}

	return tea.Batch(cmds...)
}

// setKittyTransport requests how Kitty frames are delivered. The widget's
// re-render commands are dropped: this is set before the first preview.
func (m *model) setKittyTransport(t padui.KittyTransport) {
	cfg := t.Configure(picture.Config{})
	m.transport = t
	m.pic.SetKittyFormat(cfg.KittyFormat)
	m.pic.SetKittyMedium(cfg.KittyMedium)
}

// shutdown releases what the model owns once the program has stopped. The
// model owns the engine and session after Init's goroutine fires, so they are
// closed here in order.
func (m *model) shutdown() {
	// Unlink shared-memory frames the terminal has not consumed; they would
	// otherwise persist after exit.
	m.pic.SetImage(nil)
	m.gen.StopAndWait()
	if m.session != nil {
		m.session.Close()
	}
	if m.engine != nil {
		m.engine.Close()
	}
	if m.luaDiag != nil {
		m.luaDiag.Close(context.Background())
	}
	// Best-effort autosave of current world for recovery.
	if len(m.w.Names()) > 0 {
		_ = m.w.Save(filepath.Join(os.TempDir(), "cadpad-last.cad.json"))
	}
}
