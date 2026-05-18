package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go"
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
	pflag.IntVar(&ctxSize, "ctx", 32768, "context window size in tokens")
	pflag.StringVar(&backend, "backend", "metal", "inference backend: metal, cuda, cpu")
	pflag.BoolVarP(&debug, "debug", "d", false, "log raw LLM token stream (escaped) to play.log")
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
	engOpts := ds4.EngineOptions{ModelPath: modelPath, Backend: be}
	ds4.ApplyMTPDefaults(&engOpts)
	mtpPath := engOpts.MTPPath

	// ds4.NewEngine does its own default-library search, so an explicit --lib
	// must open the engine through that specific library directly.
	var engine *ds4.Engine
	var err error
	if libPath != "" {
		lib, err := ds4.Load(libPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "load lib:", err)
			os.Exit(1)
		}
		ds4.SetDefaultLibrary(lib)
		engine, err = lib.NewEngine(engOpts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine:", ds4.EnrichEngineOpenError(err))
			os.Exit(1)
		}
	} else {
		engine, err = ds4.NewEngine(engOpts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "engine:", err)
			os.Exit(1)
		}
	}
	defer engine.Close()

	session, err := engine.NewSession(ctxSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, "session:", err)
		os.Exit(1)
	}
	defer session.Close()

	logf, err := os.OpenFile("play.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open play.log:", err)
		os.Exit(1)
	}
	defer logf.Close()
	logger := log.New(logf, "", log.Ldate|log.Ltime|log.Lmicroseconds)
	hasMTP := engine.HasMTP()
	mtpDraft := engine.MTPDraftTokens()
	logger.Printf("=== session start  model=%s backend=%s ctx=%d debug=%v mtp=%v mtpDraft=%d ===",
		filepath.Base(modelPath), backend, ctxSize, debug, hasMTP, mtpDraft)

	m := newModel(engine, session, modelPath, mtpPath, hasMTP, mtpDraft, backend, logger, debug)
	p := tea.NewProgram(m)
	runErr := func() error {
		_, err := p.Run()
		return err
	}()

	logger.Printf("=== session end ===")
	if runErr != nil {
		logger.Printf("error: %v", runErr)
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
