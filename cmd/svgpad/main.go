// Command ds4go-svgpad is an interactive TUI scratchpad for drawing SVG
// images with a locally-run DeepSeek model, rendered live in the terminal.
package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/spf13/pflag"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	flags := appinit.RegisterFlags(pflag.CommandLine, "svgpad", appinit.Defaults{})
	pflag.Parse()

	app, err := appinit.Bootstrap(flags)
	if err != nil {
		return err
	}
	defer func() { app.Close(err) }()

	m := newModel(app)
	p := tea.NewProgram(m)
	final, runErr := p.Run()

	// The model owns the engine/session once Init's goroutine fires; on
	// exit we recover them from the final model state and close in order.
	// The metadata-enrichment goroutines must finish before the session
	// closes underneath them.
	if fm, ok := final.(model); ok {
		if fm.metadataCancel != nil {
			fm.metadataCancel()
		}
		if fm.metadataWG != nil {
			fm.metadataWG.Wait()
		}
		if fm.session != nil {
			fm.session.Close()
		}
		if fm.engine != nil {
			fm.engine.Close()
		}
	}
	return runErr
}
