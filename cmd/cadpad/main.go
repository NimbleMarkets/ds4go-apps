// Command ds4go-cadpad is an interactive TUI + headless-capable CAD modeling
// scratchpad driven by LLM tool calling against the simplesdf geometry engine.
//
// It supports both rich Bubble Tea interaction (with live ntcharts picture
// previews) and pure-Go usage via its exported harness for tool registration.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/spf13/pflag"
)

func main() {
	var (
		modelPath string
		libPath   string
		ctxSize   int
		backend   string
		debug     bool
		power     int
		noEngine  bool // run without LLM for pure geometry / headless tool use
		mtpPath   string
	)
	pflag.StringVarP(&modelPath, "model", "m", "", "path to GGUF model (default $DS4_DIR/models/ds4flash.gguf)")
	pflag.StringVar(&libPath, "lib", "", "path to libds4 (optional)")
	pflag.IntVar(&ctxSize, "ctx", 16384, "context size (smaller is fine for tool use)")
	pflag.StringVar(&backend, "backend", "", "metal|cuda|cpu (default: auto)")
	pflag.StringVar(&mtpPath, "mtp", "none", "path to MTP companion GGUF model (default: none, empty or non-existent falls back to auto)")
	pflag.BoolVarP(&debug, "debug", "d", false, "tee engine logs to cadpad.log")
	pflag.IntVar(&power, "power", 80, "GPU power % (1-100)")
	pflag.BoolVar(&noEngine, "no-engine", false, "start without LLM engine (pure geometry mode or harness embedding)")
	pflag.Parse()

	if power < 1 || power > 100 {
		fmt.Fprintln(os.Stderr, "error: power 1..100")
		os.Exit(1)
	}

	if modelPath == "" {
		modelPath = ds4.DefaultModelPath()
	}
	needEngine := !noEngine
	if needEngine {
		if st, err := os.Stat(modelPath); err != nil || st.IsDir() || st.Size() == 0 {
			fmt.Fprintf(os.Stderr, "error: model not found at %s (use --no-engine for pure geometry)\n", modelPath)
			fmt.Fprintln(os.Stderr, "Run: ds4go model download q2-imatrix")
			os.Exit(1)
		}
	}

	var be ds4.Backend
	switch backend {
	case "cuda":
		be = ds4.BackendCUDA
	case "cpu":
		be = ds4.BackendCPU
	case "metal":
		be = ds4.BackendMetal
	default:
		be = ds4.DetectDefaultBackend(libPath)
	}

	logf, err := os.OpenFile("cadpad.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open cadpad.log:", err)
		os.Exit(1)
	}
	defer logf.Close()
	logger := log.New(logf, "", log.Ldate|log.Ltime|log.Lmicroseconds)

	var lib *ds4.Library
	if needEngine || libPath != "" {
		lib, err = ds4.Load(libPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "load lib:", err)
			os.Exit(1)
		}
		ds4.SetDefaultLibrary(lib)
	}

	// Capture libds4's diagnostic stream into the in-memory ring (viewable via
	// ctrl+n; teed to cadpad.log with --debug). CaptureStderr bridges libds4's
	// fd redirect to the io.Writer ring; its own Metal/CUDA banners flow here too.
	logBuf := ds4log.NewBuffer(400)
	if debug {
		logBuf.SetTee(logf)
	}
	logCap, err := ds4.CaptureStderr(logBuf)
	if err != nil {
		logger.Printf("warn CaptureStderr: %v", err)
	}

	var mtpPathResolved string
	if mtpPath != "none" {
		if mtpPath == "" {
			mtpPathResolved = ds4.DefaultMTPPath()
		} else if st, err := os.Stat(mtpPath); err == nil && !st.IsDir() && st.Size() > 0 {
			mtpPathResolved = mtpPath
		} else {
			mtpPathResolved = ds4.DefaultMTPPath()
		}
	}

	engOpts := ds4.EngineOptions{
		ModelPath:    modelPath,
		MTPPath:      mtpPathResolved,
		Backend:      be,
		WarmWeights:  true,
		PowerPercent: power,
	}
	if mtpPathResolved != "" {
		ds4.ApplyMTPDefaults(&engOpts)
	}

	logger.Printf("=== cadpad start backend=%s no-engine=%v ===", backend, noEngine)

	// In pure no-engine mode lib remains nil. newModel + Init() deliberately
	// skip all engine open work so we never touch a nil library.
	m := newModel(lib, engOpts, ctxSize, modelPath, backend, logger, logBuf, debug)
	p := tea.NewProgram(m)
	final, runErr := p.Run()

	if fm, ok := final.(model); ok {
		if fm.session != nil {
			fm.session.Close()
		}
		if fm.engine != nil {
			fm.engine.Close()
		}
		if fm.luaDiag != nil {
			fm.luaDiag.Close(context.Background())
		}
		// Best-effort autosave of current world for recovery.
		if len(fm.w.Names()) > 0 {
			_ = fm.w.Save(filepath.Join(os.TempDir(), "cadpad-last.cad.json"))
		}
	}

	// Stop capturing and drain any final libds4 diagnostics into the ring/tee.
	if logCap != nil {
		_ = logCap.Close()
	}

	logger.Printf("=== cadpad end ===")
	if runErr != nil {
		logger.Printf("error: %v", runErr)
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}

// Exported helpers for harness users / headless embedding.
func NewEmptyWorld() *world.World { return world.NewWorld() }
