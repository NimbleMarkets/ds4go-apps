// Package appinit centralizes the shared bootstrap for the ds4go pad TUIs
// (glyphpad, svgpad, cadpad): flag registration, libds4 loading, log-file
// and diagnostics-capture wiring, and ds4.EngineOptions construction.
//
// Apps call RegisterFlags, register their own extra flags, parse, then call
// Bootstrap. The returned App bundle is what each app's model constructor
// consumes; later phases (generation unification, HTTP backends) extend App
// rather than the per-app mains.
package appinit

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/spf13/pflag"
)

// Defaults carries per-app flag defaults. Zero or negative values fall back to the
// common defaults (ctx 32768, power 100).
type Defaults struct {
	Ctx   int
	Power int
}

// Flags holds the shared pad flag values populated by fs.Parse.
type Flags struct {
	Model   string
	Lib     string
	Ctx     int
	Backend string
	MTP     string
	Debug   bool
	Power   int

	app string // app name, used for log naming and help text
}

// RegisterFlags registers the seven shared pad flags on fs with per-app
// defaults. Apps register their own extra flags before calling fs.Parse.
func RegisterFlags(fs *pflag.FlagSet, app string, d Defaults) *Flags {
	if d.Ctx <= 0 {
		d.Ctx = 32768
	}
	if d.Power <= 0 {
		d.Power = 100
	}
	f := &Flags{app: app}
	fs.StringVarP(&f.Model, "model", "m", "", "path to GGUF model file (default: $DS4_DIR/models/ds4flash.gguf)")
	fs.StringVar(&f.Lib, "lib", "", "path to libds4 shared library (optional, uses default search)")
	fs.IntVar(&f.Ctx, "ctx", d.Ctx, "context window size in tokens; lower to 16384 or 8192 if VRAM is tight")
	fs.StringVar(&f.Backend, "backend", "", "inference backend: metal, cuda, cpu (default: auto)")
	fs.StringVar(&f.MTP, "mtp", "none", "path to MTP companion GGUF model (default: none, empty or non-existent falls back to auto)")
	fs.BoolVarP(&f.Debug, "debug", "d", false, fmt.Sprintf("log raw LLM traffic and tee libds4 diagnostics to %s.log", app))
	fs.IntVar(&f.Power, "power", d.Power, "GPU power duty-cycle throttle percentage (1..100)")
	return f
}

// selectBackend resolves the --backend string, defaulting to the detected
// backend for the library at libPath.
func selectBackend(name, libPath string) ds4.Backend {
	switch name {
	case "cuda":
		return ds4.BackendCUDA
	case "cpu":
		return ds4.BackendCPU
	case "metal":
		return ds4.BackendMetal
	default:
		return ds4.DetectDefaultBackend(libPath)
	}
}

// backendName renders a ds4.Backend for log lines (ds4.Backend has no
// Stringer; ds4.BackendName needs a loaded library).
func backendName(b ds4.Backend) string {
	switch b {
	case ds4.BackendMetal:
		return "metal"
	case ds4.BackendCUDA:
		return "cuda"
	case ds4.BackendCPU:
		return "cpu"
	default:
		return fmt.Sprintf("backend(%d)", int(b))
	}
}

// resolveMTP maps the --mtp flag to an MTP model path: "none" disables MTP,
// empty selects the default path, and an invalid path falls back to the
// default path.
func resolveMTP(mtp string) string {
	if mtp == "none" {
		return ""
	}
	if mtp == "" {
		return ds4.DefaultMTPPath()
	}
	if st, err := os.Stat(mtp); err == nil && !st.IsDir() && st.Size() > 0 {
		return mtp
	}
	return ds4.DefaultMTPPath()
}

type config struct {
	noEngine bool
}

// Option customizes Bootstrap.
type Option func(*config)

// WithoutEngine skips model validation and library loading (unless --lib was
// given explicitly). Used by cadpad's --no-engine mode.
func WithoutEngine(noEngine bool) Option {
	return func(c *config) { c.noEngine = noEngine }
}

// App bundles everything Bootstrap sets up. Each pad's model constructor
// takes it as its single parameter; later phases (generation unification,
// HTTP backends) extend App rather than the apps' mains.
type App struct {
	Name       string
	Flags      *Flags
	Lib        *ds4.Library      // nil in no-engine mode
	EngineOpts ds4.EngineOptions // MTP resolved, ApplyMTPDefaults applied
	ModelInfo  *ds4.ModelInfo    // catalog identity behind ModelPath; nil when not catalog-managed
	Logger     *log.Logger       // writes to <app>.log
	LogBuf     *ds4log.Buffer    // libds4 diagnostics ring; teed to the log file with --debug

	logf   *os.File
	logCap io.Closer
}

// Bootstrap performs the side effects shared by the pad mains: validation,
// backend selection, log file + diagnostics capture, library load, and
// engine options. On error it closes anything it opened and returns nil,
// err. It never calls os.Exit.
func Bootstrap(f *Flags, opts ...Option) (*App, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}

	if f.Power < 1 || f.Power > 100 {
		return nil, fmt.Errorf("power must be between 1 and 100")
	}

	modelPath := f.Model
	if modelPath == "" {
		modelPath = ds4.DefaultModelPath()
	}
	needEngine := !cfg.noEngine
	if needEngine {
		if st, err := os.Stat(modelPath); err != nil || st.IsDir() || st.Size() == 0 {
			return nil, fmt.Errorf("model not found at %s\nRun: ds4go model download q2-imatrix", modelPath)
		}
	}

	logName := f.app + ".log"
	logf, err := os.OpenFile(logName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", logName, err)
	}
	logger := log.New(logf, "", log.Ldate|log.Ltime|log.Lmicroseconds)

	// Load libds4 explicitly — rather than letting ds4.NewEngine load the
	// default — so an explicit --lib is honored and the library instance is
	// set as default before configuring any stderr capture. In pure
	// no-engine mode lib stays nil and the apps skip all engine-open work.
	var lib *ds4.Library
	if needEngine || f.Lib != "" {
		lib, err = ds4.Load(f.Lib)
		if err != nil {
			logf.Close()
			return nil, fmt.Errorf("load lib: %w", err)
		}
		ds4.SetDefaultLibrary(lib)
	}

	// Capture libds4's diagnostic stream into the in-memory ring BEFORE any
	// engine work happens — the GPU startup banner lands in the apps' log
	// overlay instead of the terminal, and is teed to <app>.log with --debug.
	logBuf := ds4log.NewBuffer(500)
	if f.Debug {
		logBuf.SetTee(logf)
	}
	app := &App{
		Name:   f.app,
		Flags:  f,
		Lib:    lib,
		Logger: logger,
		LogBuf: logBuf,
		logf:   logf,
	}
	if logCap, err := ds4.CaptureStderr(logBuf); err != nil {
		logger.Printf("warn: ds4.CaptureStderr: %v", err)
	} else {
		app.logCap = logCap
	}

	app.EngineOpts = ds4.EngineOptions{
		ModelPath:    modelPath,
		MTPPath:      resolveMTP(f.MTP),
		Backend:      selectBackend(f.Backend, f.Lib),
		WarmWeights:  true,
		PowerPercent: f.Power,
	}
	if app.EngineOpts.MTPPath != "" {
		ds4.ApplyMTPDefaults(&app.EngineOpts)
	}

	// The default model reaches apps as the stable ds4flash.gguf link, so
	// resolve the catalog identity behind the path for display/provenance.
	modelLabel := filepath.Base(modelPath)
	if info, ok := ds4.ResolveModelInfo(modelPath); ok {
		app.ModelInfo = &info
		modelLabel = info.Alias
	}

	logger.Printf("=== %s start  model=%s backend=%s ctx=%d debug=%v ===",
		f.app, modelLabel, backendName(app.EngineOpts.Backend), f.Ctx, f.Debug)
	return app, nil
}

// Close logs runErr if non-nil, drains and closes the OS pipe capture, writes
// the end marker, and closes the log file. It is safe on a
// literal-constructed App (nil internals), so tests can build App directly.
func (a *App) Close(runErr error) {
	if runErr != nil && a.Logger != nil {
		a.Logger.Printf("error: %v", runErr)
	}
	if a.logCap != nil {
		_ = a.logCap.Close()
	}
	if a.Logger != nil {
		a.Logger.Printf("=== %s end ===", a.Name)
	}
	if a.logf != nil {
		_ = a.logf.Close()
	}
}
