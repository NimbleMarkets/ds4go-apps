package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/spf13/pflag"
)

func main() {
	var (
		modelPath string
		libPath   string
		ctxSize   int
		backend   string
		mtpPath   string
		debug     bool
		power     int
	)
	pflag.StringVarP(&modelPath, "model", "m", "", "path to GGUF model file (default: $DS4_DIR/models/ds4flash.gguf)")
	pflag.StringVar(&libPath, "lib", "", "path to libds4 shared library (optional, uses default search)")
	pflag.IntVar(&ctxSize, "ctx", 32768, "context window size in tokens; lower to 16384 or 8192 if VRAM is tight")
	pflag.StringVar(&backend, "backend", "", "inference backend: metal, cuda, cpu (default: auto)")
	pflag.StringVar(&mtpPath, "mtp", "none", "path to MTP companion GGUF model (default: none, empty or non-existent falls back to auto)")
	pflag.BoolVarP(&debug, "debug", "d", false, "log raw LLM token stream and tee libds4 diagnostics to glyphpad.log")
	pflag.IntVar(&power, "power", 100, "GPU power duty-cycle throttle percentage (1..100)")
	pflag.Parse()

	if power < 1 || power > 100 {
		fmt.Fprintln(os.Stderr, "error: power must be between 1 and 100")
		os.Exit(1)
	}

	if modelPath == "" {
		modelPath = ds4.DefaultModelPath()
	}
	if st, err := os.Stat(modelPath); err != nil || st.IsDir() || st.Size() == 0 {
		fmt.Fprintf(os.Stderr, "error: model not found at %s\n", modelPath)
		fmt.Fprintln(os.Stderr, "Run: ds4go model download q2-imatrix")
		os.Exit(1)
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
	logf, err := os.OpenFile("glyphpad.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open glyphpad.log:", err)
		os.Exit(1)
	}
	defer logf.Close()
	logger := log.New(logf, "", log.Ldate|log.Ltime|log.Lmicroseconds)

	// Load the libds4 shared library now (fast, just dlopen). We do this
	// explicitly — rather than letting ds4.NewEngine load the default —
	// so an explicit --lib is honored and the library instance is set
	// as default before configuring any stderr capture.
	lib, err := ds4.Load(libPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load lib:", err)
		os.Exit(1)
	}
	ds4.SetDefaultLibrary(lib)

	// Capture libds4's diagnostic stream into the in-memory ring BEFORE any
	// engine work happens — the GPU startup banner lands there (viewable via
	// ctrl+n) instead of the terminal, and is teed into glyphpad.log with
	// --debug. CaptureStderr bridges libds4's fd redirect to the io.Writer ring;
	// libds4's own Metal/CUDA banners now flow here too, not raw fd 2.
	logBuf := ds4log.NewBuffer(500)
	if debug {
		logBuf.SetTee(logf)
	}
	logCap, err := ds4.CaptureStderr(logBuf)
	if err != nil {
		logger.Printf("warn: ds4.CaptureStderr: %v", err)
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

	logger.Printf("=== session start  model=%s backend=%s ctx=%d debug=%v ===",
		filepath.Base(modelPath), backend, ctxSize, debug)

	m := newModel(lib, engOpts, ctxSize, modelPath, engOpts.MTPPath, backend, logger, logBuf, debug)
	p := tea.NewProgram(m)
	final, runErr := p.Run()

	// The model owns the engine/session once Init's goroutine fires; on
	// exit we recover them from the final model state and close in order.
	if fm, ok := final.(model); ok {
		if fm.session != nil {
			fm.session.Close()
		}
		if fm.engine != nil {
			fm.engine.Close()
		}
	}

	// Stop capturing and drain any final libds4 diagnostics into the ring/tee.
	if logCap != nil {
		_ = logCap.Close()
	}

	logger.Printf("=== session end ===")
	if runErr != nil {
		logger.Printf("error: %v", runErr)
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
