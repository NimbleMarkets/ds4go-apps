package main

import (
	"errors"
	"fmt"
	"image"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/modelpicker"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go-apps/internal/runconfig"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/library"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/memory"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/wgslls"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

type tickMsg time.Time

// frameDueMsg starts the next animation frame once the frame period has
// elapsed since the previous frame started.
type frameDueMsg struct{}
type startupMsg struct{}
type probeMsg struct{}
type frameMsg struct {
	img        *image.NRGBA
	err        error
	duration   time.Duration
	revision   uint64
	cols, rows int
}
type encodedMsg struct {
	msg         tea.Msg
	performance tools.Performance
}
type presentedMsg struct{}
type actionMsg struct {
	text string
	err  error
}
type driverMsg struct{ event bubble.Event }
type doneMsg struct {
	notice string
	result bubble.RunResult
	err    error
}
type engineMsg struct{ result engineinit.Result }

type model struct {
	language                                    *wgslls.Service
	memory                                      *memory.Memory
	memoryText                                  string
	contextUsage                                bubble.ContextUsageEvent
	compactions                                 int
	state                                       *tools.State
	app                                         *appinit.App
	options                                     runconfig.Options
	pic                                         picture.Model
	transport                                   padui.KittyTransport
	transportFallbackLogged                     bool
	input                                       textinput.Model
	picker                                      modelpicker.Model
	loading                                     padui.Loading
	engineStatus                                engineinit.Status
	modelPath                                   string
	modelInfo                                   *ds4.ModelInfo
	engOpts                                     ds4.EngineOptions
	quitPending                                 bool
	showLog                                     bool
	logTop                                      int
	loadLogMarker                               string
	inspect                                     inspector
	thinkMode, activeThinkMode                  ds4.ThinkMode
	activity                                    []activityRound
	toolStats                                   toolStats
	library                                     *library.Store
	gallery                                     shaderGallery
	hasVision                                   bool
	width, height, selected, starter, downscale int
	rasterW, rasterH                            int
	frameStart                                  time.Time
	targetFPS                                   int
	fullscreen                                  bool
	playing, dirty, rendering, noEngine         bool
	lastTick, lastFrame                         time.Time
	t, dt                                       float32
	frame                                       uint32
	fps                                         float64
	lastRevision                                uint64
	status                                      string
	log                                         []string
	gen, loader, action                         *bubble.Generation
	// The loader owns this holder until it sends engineMsg or StopAndWait returns.
	loaded  *engineinit.Result
	engine  *ds4.Engine
	session *ds4.Session
	history []ds4.ChatMessage
}

func newModel(s *tools.State) *model {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "Enter a prompt or /help (Tab to focus)"
	input.CharLimit = 8192
	return &model{engineStatus: engineinit.StatusDormant, state: s, input: input, pic: picture.NewWithConfig(picture.Config{CellPixelWidth: 8, CellPixelHeight: 16, Fit: picture.FitFill}), playing: true, dirty: true, downscale: 2, targetFPS: 60, status: "Space pause · ↑↓ select · ←→ adjust · Tab prompt · ? help", options: runconfig.Options{Temperature: .7, TopP: .95, ToolRounds: 20}}
}

// setKittyTransport requests how Kitty frames are delivered. The widget's
// re-render commands are dropped: the animation presents a new frame anyway.
func (m *model) setKittyTransport(t padui.KittyTransport) {
	cfg := t.Configure(picture.Config{})
	m.transport, m.transportFallbackLogged = t, false
	m.pic.SetKittyFormat(cfg.KittyFormat)
	m.pic.SetKittyMedium(cfg.KittyMedium)
}
func (m *model) tick() tea.Cmd {
	return tea.Tick(time.Second/time.Duration(max(1, m.targetFPS)), func(t time.Time) tea.Msg { return tickMsg(t) })
}
func (m *model) Init() tea.Cmd {
	return tea.Batch(m.pic.Init(), picture.RequestCellSize(), picture.QueryKittySupport(), m.tick(), func() tea.Msg { return startupMsg{} }, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return probeMsg{} }))
}
func (m *model) close() {
	// Unlink shared-memory frames the terminal has not consumed; they would
	// otherwise persist after exit.
	m.pic.SetImage(nil)
	m.gen.StopAndWait()
	m.action.StopAndWait()
	m.loader.StopAndWait()
	m.gallery.preview.StopAndWait()
	m.language.Close()
	if err := m.state.Checkpoint(); err != nil {
		m.addLog("Could not save current shader: " + err.Error())
	}
	if m.loaded != nil {
		if m.loaded.Session != nil {
			m.loaded.Session.Close()
		}
		if m.loaded.Engine != nil {
			m.loaded.Engine.Close()
		}
	}
}
func (m *model) renderCmd() tea.Cmd {
	if m.rendering || m.width == 0 || !m.dirty || m.picker.IsOpen() || m.showLog || m.inspect.kind != "" || m.gallery.open {
		return nil
	}
	m.rendering = true
	m.dirty = false
	m.frameStart = time.Now()
	snap := m.state.Snapshot()
	m.lastRevision = snap.Revision
	cols, rows := m.viewport()
	cw, ch := m.pic.CellPixelSize()
	// Match the GPU raster to the widget's upload size exactly. Otherwise it
	// expands our downscaled image on the CPU only to PNG-encode extra pixels.
	factor, w, h := viewportResolution(cols, rows, cw, ch, m.downscale)
	m.pic.SetKittyResolutionFactor(factor) // next SetImage encodes the new frame
	m.rasterW, m.rasterH = w, h
	m.state.Viewport(w, h)
	state := m.state
	return func() tea.Msg {
		start := time.Now()
		img, err := state.Render(snap, w, h)
		return frameMsg{img: img, err: err, duration: time.Since(start), revision: snap.Revision, cols: cols, rows: rows}
	}
}
func (m *model) addLog(s string) {
	if m.app != nil && m.app.Logger != nil {
		m.app.Logger.Print(s)
	}
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			m.log = append(m.log, line)
		}
	}
	if len(m.log) > 200 {
		m.log = append([]string(nil), m.log[len(m.log)-200:]...)
	}
}
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case memoryViewMsg:
		m.action.StopAndWait()
		m.action = nil
		if msg.err != nil {
			m.status = msg.err.Error()
			break
		}
		m.memoryText = msg.text
		m.inspect = inspector{kind: "memory"}
	case compactedMsg:
		m.action.StopAndWait()
		m.action = nil
		if msg.err != nil {
			m.status = msg.err.Error()
			break
		}
		m.history = msg.history
		m.compactions++
		m.contextUsage = bubble.ContextUsageEvent{}
		m.status = "Context checkpoint saved · prompt usage recalculated on next request"
	case galleryLoadedMsg:
		if !m.gallery.open || msg.request != m.gallery.request {
			break
		}
		m.gallery.loading = false
		m.gallery.entries = msg.entries
		if msg.err != nil {
			m.gallery.note = msg.err.Error()
			m.addLog("Gallery: " + msg.err.Error())
		}
		if len(msg.warnings) > 0 {
			m.gallery.note = fmt.Sprintf("Skipped %d unreadable presets · Ctrl+N logs after closing", len(msg.warnings))
			for _, warning := range msg.warnings {
				m.addLog("Gallery: " + warning)
			}
		}
		cmds = append(cmds, m.galleryPreview())
	case galleryPreviewMsg:
		m.gallery.preview.StopAndWait()
		m.gallery.preview = nil
		if e, ok := m.selectedGalleryEntry(); m.gallery.open && ok && e.Path == msg.path {
			m.gallery.image = msg.img
			m.gallery.thumbnail = ""
			if msg.err != nil {
				m.gallery.previewError = msg.err.Error()
			}
		} else {
			cmds = append(cmds, m.galleryPreview())
		}
	case startupMsg:
		cmds = append(cmds, m.startupModel())
	case modelpicker.LoadedMsg:
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		cmds = append(cmds, cmd)
	case modelpicker.SelectedMsg:
		cmds = append(cmds, m.switchModel(msg.Model))
	case padui.LoadingTick:
		cmds = append(cmds, m.loading.Update(msg))
	case engineMsg:
		m.loader.StopAndWait()
		m.loader = nil
		m.loading.Active = false
		m.engine, m.session = msg.result.Engine, msg.result.Session
		if msg.result.Err != nil {
			m.engineStatus = engineinit.StatusError
			m.status = "Could not load " + m.modelDisplayName() + ": " + msg.result.Err.Error() + " · Ctrl+N details"
			m.addLog(m.status)
			// Native stderr arrives asynchronously; the dialog reads the live
			// buffer so details remain visible even if its pump finishes later.
			m.showLog, m.logTop = true, -1
		} else {
			m.engineStatus = engineinit.StatusReady
			m.hasVision = m.engine.HasVision()
			m.status = "Ready · " + m.modelDisplayName()
			m.addLog(m.status)
			if !m.hasVision {
				m.addLog("Vision unavailable: trip_preview disabled; shader and parameter tools are ready.")
			}
		}
		if m.quitPending {
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		cmds = append(cmds, m.resizeViewport())
	case tickMsg:
		m.advanceClock(time.Time(msg))
		m.selected = min(m.selected, max(0, len(m.state.Snapshot().Source.Params)-1))
		if m.state.Snapshot().Revision != m.lastRevision {
			m.dirty = true
		}
		cmds = append(cmds, m.tick(), m.renderCmd())
	case frameMsg:
		if msg.err != nil {
			m.rendering = false
			m.playing = false
			m.dirty = false
			m.status = "GPU: " + msg.err.Error()
			m.addLog(m.status)
			break
		}
		// A preset/parameter edit can supersede a frame while GPU work is in flight.
		cols, rows := m.viewport()
		if msg.revision != m.state.Snapshot().Revision || (msg.cols > 0 && (msg.cols != cols || msg.rows != rows)) {
			m.rendering = false
			m.dirty = true
			break
		}
		m.frame++
		now := time.Now()
		if !m.lastFrame.IsZero() {
			fps := 1 / now.Sub(m.lastFrame).Seconds()
			if m.fps == 0 {
				m.fps = fps
			} else {
				m.fps = .9*m.fps + .1*fps
			}
		}
		m.lastFrame = now
		if msg.duration > 70*time.Millisecond && m.downscale < 8 {
			m.downscale++
			m.dirty = true
		}
		cmd := m.pic.SetImage(msg.img)
		if cmd != nil {
			cmds = append(cmds, func() tea.Msg {
				start := time.Now()
				encoded := cmd()
				perf := tools.Performance{RenderMS: float64(msg.duration) / float64(time.Millisecond), EncodeMS: float64(time.Since(start)) / float64(time.Millisecond), SampledAt: time.Now().UnixMilli()}
				if frame, ok := encoded.(picture.KittyFrameMsg); ok {
					perf.UploadBytes = len(frame.APC)
					perf.Kitty = true
					perf.Transport = string(padui.FrameTransport(frame))
				}
				return encodedMsg{msg: encoded, performance: perf}
			})
		} else {
			m.state.RecordPerformance(tools.Performance{RenderMS: float64(msg.duration) / float64(time.Millisecond), SampledAt: time.Now().UnixMilli()})
			cmds = append(cmds, m.releaseFrame())
		}
	case encodedMsg:
		m.state.RecordPerformance(msg.performance)
		if m.transport == padui.KittyTransportSharedMemory && msg.performance.Kitty && msg.performance.Transport != string(m.transport) && !m.transportFallbackLogged {
			m.transportFallbackLogged = true
			m.addLog("Kitty shared memory unavailable; frames use direct " + msg.performance.Transport)
		}
		// Keep coalescing until the upload and placeholder update have been
		// sent, so a later frame cannot overtake this terminal presentation.
		cmds = append(cmds, tea.Sequence(m.pic.Update(msg.msg), func() tea.Msg { return presentedMsg{} }))
	case presentedMsg:
		cmds = append(cmds, m.releaseFrame())
	case frameDueMsg:
		if m.playing {
			cmds = append(cmds, m.scheduleFrame())
		}
	case actionMsg:
		m.action.StopAndWait()
		m.action = nil
		if msg.err != nil {
			m.status = msg.err.Error()
		} else {
			m.status = msg.text
			m.selected = 0
			m.dirty = true
		}
		m.addLog(m.status)
	case driverMsg:
		switch ev := msg.event.(type) {
		case bubble.ContextUsageEvent:
			m.contextUsage = ev
		case bubble.ContextCompactedEvent:
			m.compactions++
			m.addLog(fmt.Sprintf("Context compacted: %d → %d prompt tokens; checkpoint saved", ev.Before, ev.After))
		case bubble.StreamEvent:
			m.applyStream(ev.Event)
		case bubble.LogEvent:
			m.addLog(ev.Message)
		case bubble.ToolResultsEvent:
			m.recordToolResults(ev.Results)
			for _, r := range ev.Results {
				m.addLog(r.Content)
				for _, part := range r.Parts {
					if part.Text != "" {
						m.addLog(part.Text)
					}
					if part.Image != nil {
						m.addLog("[preview image returned]")
					}
				}
			}
		case bubble.AssistantMessageEvent:
			m.finishActivity(ev.Message)
			m.recordToolCalls(ev.Message.ToolCalls)
			if ev.Message.Content != "" {
				m.addLog(ev.Message.Content)
			}
		case bubble.RoundStartedEvent:
			m.startActivityRound(ev.Round)
			m.status = fmt.Sprintf("Generating · tool round %d/%d", ev.Round+1, m.options.ToolRounds)
			if ev.Round >= m.options.ToolRounds {
				m.status = "Finishing · tool budget exhausted"
			}
		case bubble.MalformedRetryEvent:
			m.addLog("Retrying malformed model tool call: " + ev.Reason)
		}
		cmds = append(cmds, m.gen.Wait())
	case doneMsg:
		m.gen.StopAndWait()
		m.gen = nil
		m.abandonPendingTools()
		m.history = msg.result.History
		if msg.notice != "" {
			m.finishActivity(msg.result.Assistant)
			m.addLog(msg.result.Assistant.Content)
		}
		m.dirty = true
		if msg.err != nil {
			m.status = msg.err.Error()
			if errors.Is(msg.err, bubble.ErrContextBudget) || errors.Is(msg.err, ds4.ErrContextFull) {
				m.status = "Context is full; work retained. Try /compact, a shorter request, or a larger --ctx."
			}
			m.addLog(m.status)
		} else if msg.notice != "" {
			m.status = msg.notice
			m.addLog(msg.notice)
		} else {
			m.status = "Ready"
		}
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, m.requestQuit()
		}
		if m.picker.IsOpen() {
			var cmd tea.Cmd
			m.picker, cmd = m.picker.Update(msg)
			return m, cmd
		}
		if m.gallery.open {
			return m, m.galleryKey(msg)
		}
		if msg.String() == "f3" {
			return m, m.openGallery()
		}
		if handled, cmd := m.inspectorKey(msg); handled {
			return m, cmd
		}
		if m.logKey(msg) {
			return m, nil
		}
		return m, m.key(msg)
	}
	cmds = append(cmds, m.pic.Update(msg))
	if m.input.Focused() && !m.fullscreen && !m.picker.IsOpen() && !m.showLog && m.inspect.kind == "" && !m.gallery.open {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}
	if picture.KittySupported() == picture.KittyCapabilitySupported && m.pic.Mode() == picture.PictureGlyph {
		cmds = append(cmds, m.pic.Toggle())
	}
	return m, tea.Batch(cmds...)
}
func (m *model) View() tea.View {
	if m.picker.IsOpen() {
		v := tea.NewView(lipgloss.Place(max(1, m.width), max(1, m.height), lipgloss.Center, lipgloss.Center, m.picker.View(m.width, m.height)))
		v.AltScreen = true
		return v
	}
	if m.gallery.open {
		v := tea.NewView(m.galleryView())
		v.AltScreen = true
		return v
	}
	if m.showLog {
		lines := m.logLines()
		v := tea.NewView(padui.Pager(m.width, m.height, "Logs · "+m.modelDisplayName(), lines, m.logTop, "↑↓/jk · Space/b or PgDn/PgUp page · g/G start/follow · Esc/q/Ctrl+N close"))
		v.AltScreen = true
		return v
	}
	if m.inspect.kind != "" {
		v := tea.NewView(m.inspectorView())
		v.AltScreen = true
		return v
	}
	if m.fullscreen {
		v := tea.NewView(fitCells(m.pic.View().Content, m.width, m.height))
		v.AltScreen = true
		return v
	}
	if m.width < 48 || m.height < 16 {
		v := tea.NewView("trippad — enlarge terminal to at least 48×16 (Ctrl-C quits)")
		v.AltScreen = true
		return v
	}
	v := tea.NewView(m.workspaceView())
	v.AltScreen = true
	return v
}

// advanceClock moves animation time forward to now while playing and
// publishes the clock to the tool state.
func (m *model) advanceClock(now time.Time) {
	m.dt = 0
	if !m.lastTick.IsZero() && m.playing {
		m.dt = float32(now.Sub(m.lastTick).Seconds())
		m.t += m.dt
		m.dirty = true
	}
	m.lastTick = now
	m.state.Clock(m.t, m.dt, m.frame, m.fps)
}

// releaseFrame frees the single in-flight frame slot and, while playing,
// starts the next frame as soon as the frame period allows instead of waiting
// for the next tick. Waiting for the tick made any frame longer than one
// period cost two, snapping 60 fps to 30, 20 or 15.
func (m *model) releaseFrame() tea.Cmd {
	m.rendering = false
	if !m.playing {
		return nil
	}
	return m.scheduleFrame()
}

// scheduleFrame starts a frame now if the frame period has elapsed since the
// previous frame started, otherwise defers by the remaining time.
func (m *model) scheduleFrame() tea.Cmd {
	if m.rendering {
		return nil
	}
	period := time.Second / time.Duration(max(1, m.targetFPS))
	if wait := period - time.Since(m.frameStart); wait > 0 {
		return tea.Tick(wait, func(time.Time) tea.Msg { return frameDueMsg{} })
	}
	m.advanceClock(time.Now())
	return m.renderCmd()
}
