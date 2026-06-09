// Command ds4go-cadpad is an interactive TUI + headless-capable CAD modeling
// scratchpad driven by LLM tool calling against the simplesdf geometry engine.
//
// It supports both rich Bubble Tea interaction (with live ntcharts picture
// previews) and pure-Go usage via its exported harness for tool registration.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	"github.com/spf13/pflag"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	flags := appinit.RegisterFlags(pflag.CommandLine, "cadpad", appinit.Defaults{Ctx: 16384, Power: 80})
	var noEngine bool
	pflag.BoolVar(&noEngine, "no-engine", false, "start without LLM engine (pure geometry mode or harness embedding)")
	pflag.Parse()

	app, err := appinit.Bootstrap(flags, appinit.WithoutEngine(noEngine))
	if err != nil {
		return err
	}
	defer func() { app.Close(err) }()

	m := newModel(app)
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
		if fm.luaDiag != nil {
			fm.luaDiag.Close(context.Background())
		}
		// Best-effort autosave of current world for recovery.
		if len(fm.w.Names()) > 0 {
			_ = fm.w.Save(filepath.Join(os.TempDir(), "cadpad-last.cad.json"))
		}
	}
	return runErr
}

// Exported helpers for harness users / headless embedding.
func NewEmptyWorld() *world.World { return world.NewWorld() }
