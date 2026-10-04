// Command trippad is a live GPU shader playground with optional LLM tools.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go-apps/internal/runconfig"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/library"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/memory"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
	tripweb "github.com/NimbleMarkets/ds4go-apps/internal/trippad/web"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/wgslls"
	"github.com/NimbleMarkets/ds4go-apps/ntgpu"
	"github.com/spf13/pflag"
)

const defaultContextTokens = 128 * 1024

func main() {
	// Bubble Tea's alternate screen can hide the first (faulting) stack.
	// The runtime owns a duplicate descriptor and writes fatal reports here
	// even when ordinary application logging cannot run.
	if f, err := os.OpenFile("trippad-crash.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err != nil {
		fmt.Fprintln(os.Stderr, "Could not enable crash log:", err)
	} else {
		if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
			fmt.Fprintln(os.Stderr, "Could not enable crash log:", err)
		}
		f.Close()
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (err error) {
	flags := appinit.RegisterFlags(pflag.CommandLine, "trippad", appinit.Defaults{Ctx: defaultContextTokens, Power: 100})
	fullscreen := pflag.Bool("fullscreen", false, "start with only the animation visible (Ctrl+F or Esc returns)")
	noEngine := pflag.Bool("no-engine", false, "animate shaders without an LLM engine")
	preset := pflag.String("preset", "", "load a .trip.json preset at startup")
	downscale := pflag.Int("downscale", 2, "viewport pixel downscale (1..8; adapts upward for slow frames)")
	fps := pflag.Int("fps", 60, "target animation frames per second (1..60)")
	kittyTransport := pflag.String("kitty-transport", string(padui.KittyTransportAuto), padui.KittyTransportUsage)
	webAddr := pflag.String("web", "", "serve a WebGPU comparison on a loopback address (default 127.0.0.1:8080)")
	pflag.Lookup("web").NoOptDefVal = "127.0.0.1:8080"
	webOnly := pflag.Bool("web-only", false, "serve only the browser demo, without terminal rendering or a model")
	galleryDir := pflag.String("gallery-dir", "trippad-gallery", "directory for automatic shader history and the F3 gallery")
	wgslLSP := pflag.String("wgsl-lsp", "auto", "WGSL language server: auto, off, or executable path")
	memoryDir := pflag.String("memory-dir", "", "scratchpad root (default $DS4_DIR/scratch/trippad)")
	memorySession := pflag.String("memory-session", "", "resume scratchpad session ID (default: new session)")
	options := runconfig.Register(pflag.CommandLine, 20)
	pflag.Parse()
	if err := options.Validate(); err != nil {
		return err
	}
	if *downscale < 1 || *downscale > 8 {
		return fmt.Errorf("--downscale must be 1..8")
	}
	if *fps < 1 || *fps > 60 {
		return fmt.Errorf("--fps must be 1..60")
	}
	transport, err := padui.ParseKittyTransport(*kittyTransport)
	if err != nil {
		return fmt.Errorf("--kitty-transport: %w", err)
	}
	if *webOnly {
		if *webAddr == "" {
			*webAddr = "127.0.0.1:8080"
		}
		return serveBrowser(*webAddr, *preset, *galleryDir, *fps)
	}
	app, err := appinit.Bootstrap(flags, appinit.WithoutEngine(*noEngine), appinit.AllowModelSelection())
	if err != nil {
		return err
	}
	defer func() { app.Close(err) }()
	// Native diagnostics explain load failures even when raw LLM debug logging
	// is disabled. Keep the asynchronous stderr pump's complete output on disk.
	if app.LogBuf != nil && app.Logger != nil {
		app.LogBuf.SetTee(app.Logger.Writer())
	}
	beginLogShutdown := configureGoLogs(app.LogBuf, flags.Debug)
	pipeline := shader.NewPipeline(ntgpu.DefaultExecutor())
	defer pipeline.Close()
	state, err := tools.NewState(shader.Starters()[0], pipeline)
	if err != nil {
		return err
	}
	store, err := library.New(*galleryDir)
	if err != nil {
		return fmt.Errorf("open shader gallery: %w", err)
	}
	state.SetArchive(store.Save)
	if *preset != "" {
		if err := state.Load(*preset); err != nil {
			return err
		}
	}
	var webURL string
	if *webAddr != "" {
		server, err := tripweb.Start(*webAddr, state.Snapshot, *fps, true, store)
		if err != nil {
			return err
		}
		defer server.Close()
		webURL = server.URL
		fmt.Fprintln(os.Stderr, "Trippad WebGPU:", webURL)
	}
	mem, err := memory.Open(*memoryDir, *memorySession)
	if err != nil {
		return fmt.Errorf("open scratchpad: %w", err)
	}
	defer mem.Close()
	m := newModel(state)
	if webURL != "" {
		m.addLog("Browser comparison: " + webURL + " · load live state to copy shader and controls")
	}
	m.memory = mem
	m.addLog("Memory: " + mem.Dir + " · session " + mem.Session + " · /memory")
	m.library = store
	m.app = app
	m.options = *options
	m.language = wgslls.New(*wgslLSP)
	if m.language.Command != "" {
		m.addLog("WGSL language server: " + m.language.Command + " (starts on first shader query)")
	} else {
		m.addLog("WGSL language server unavailable/disabled; native shader validation remains available")
	}
	m.downscale = *downscale
	m.targetFPS = *fps
	m.setKittyTransport(transport)
	if !transport.Implicit() {
		m.addLog("Kitty transport: " + string(transport) + " requested")
	}
	m.noEngine = *noEngine
	m.fullscreen = *fullscreen
	m.addLog("Shader history: " + store.Dir + " · F3 gallery")
	defer func() {
		beginLogShutdown()
		m.close()
	}()
	_, err = tea.NewProgram(m).Run()
	return err
}
