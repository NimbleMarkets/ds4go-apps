package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-playground/internal/ds4log"
	"github.com/spf13/pflag"
)

func main() {
	var (
		modelPath string
		libPath   string
		ctxSize   int
		backend   string
		debug     bool
	)
	pflag.StringVarP(&modelPath, "model", "m", "", "path to GGUF model file (default: $DS4_DIR/models/ds4flash.gguf)")
	pflag.StringVar(&libPath, "lib", "", "path to libds4 shared library (optional, uses default search)")
	pflag.IntVar(&ctxSize, "ctx", 32768, "context window size in tokens; lower to 16384 or 8192 if VRAM is tight")
	pflag.StringVar(&backend, "backend", "metal", "inference backend: metal, cuda, cpu")
	pflag.BoolVarP(&debug, "debug", "d", false, "log raw LLM token stream and tee libds4 diagnostics to glyphpad.log")
	pflag.Parse()

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
	default:
		be = ds4.BackendMetal
	}
	logf, err := os.OpenFile("glyphpad.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open glyphpad.log:", err)
		os.Exit(1)
	}
	defer logf.Close()
	logger := log.New(logf, "", log.Ldate|log.Ltime|log.Lmicroseconds)

	// Install the libds4 log sink BEFORE any engine work happens — that
	// keeps the GPU startup banner out of the terminal (it lands in the
	// in-memory ring instead, viewable via ctrl+n). With --debug the
	// banner is also teed into glyphpad.log.
	logBuf := ds4log.NewBuffer(500)
	if debug {
		logBuf.SetTee(logf)
	}
	if err := ds4.SetLogOutput(logBuf); err != nil {
		logger.Printf("warn: ds4.SetLogOutput: %v", err)
	}

	// Load the libds4 shared library now (fast, just dlopen). We do this
	// explicitly — rather than letting ds4.NewEngine load the default —
	// so an explicit --lib is honored and the log callback installed
	// above is attached to the exact library that the engine will use.
	lib, err := ds4.Load(libPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load lib:", err)
		os.Exit(1)
	}
	ds4.SetDefaultLibrary(lib)

	engOpts := ds4.EngineOptions{ModelPath: modelPath, Backend: be, WarmWeights: true}
	ds4.ApplyMTPDefaults(&engOpts)
	mtpPath := engOpts.MTPPath

	logger.Printf("=== session start  model=%s backend=%s ctx=%d debug=%v ===",
		filepath.Base(modelPath), backend, ctxSize, debug)

	// Redirect stderr to /dev/null to squelch C/C++ backend library log spam
	var originalStderrFd int
	var dupErr error
	if devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		originalStderrFd, dupErr = syscall.Dup(2)
		if dupErr == nil {
			_ = syscall.Dup2(int(devNull.Fd()), 2)
		}
		devNull.Close()
	}

	m := newModel(lib, engOpts, ctxSize, modelPath, mtpPath, backend, logger, logBuf, debug)
	p := tea.NewProgram(m)
	final, runErr := p.Run()

	// Restore stderr
	if dupErr == nil {
		_ = syscall.Dup2(originalStderrFd, 2)
		_ = syscall.Close(originalStderrFd)
	}

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

	logger.Printf("=== session end ===")
	if runErr != nil {
		logger.Printf("error: %v", runErr)
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
