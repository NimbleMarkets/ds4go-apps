// Command ds4go-glyphpad is an interactive TUI scratchpad for generating and
// arranging Unicode glyph art with a locally-run DeepSeek model.
package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/runconfig"
	"github.com/spf13/pflag"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	flags := appinit.RegisterFlags(pflag.CommandLine, "glyphpad", appinit.Defaults{})
	options := runconfig.Register(pflag.CommandLine, 0)
	pflag.Parse()
	if err := options.Validate(); err != nil {
		return err
	}

	app, err := appinit.Bootstrap(flags, appinit.AllowModelSelection())
	if err != nil {
		return err
	}
	defer func() { app.Close(err) }()

	m := newModel(app)
	m.runOptions = *options
	p := tea.NewProgram(m)
	final, runErr := p.Run()

	// The model owns the engine/session once Init's goroutine fires; on
	// exit we recover them from the final model state and close in order.
	if fm, ok := final.(model); ok {
		fm.gen.StopAndWait()
		if fm.session != nil {
			fm.session.Close()
		}
		if fm.engine != nil {
			fm.engine.Close()
		}
	}
	return runErr
}
