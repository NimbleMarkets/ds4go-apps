// Command ds4go-cadpad is an interactive TUI + headless-capable CAD modeling
// scratchpad driven by LLM tool calling against the simplesdf geometry engine.
//
// It supports both rich Bubble Tea interaction (with live ntcharts picture
// previews) and pure-Go usage via its exported harness for tool registration.
package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
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
	flags := appinit.RegisterFlags(pflag.CommandLine, "cadpad", appinit.Defaults{Ctx: 16384, Power: 80})
	var noEngine bool
	pflag.BoolVar(&noEngine, "no-engine", false, "start without LLM engine (pure geometry mode or harness embedding)")
	kittyTransport := pflag.String("kitty-transport", string(padui.KittyTransportPNG), padui.KittyTransportUsage)
	options := runconfig.Register(pflag.CommandLine, 36)
	pflag.Parse()
	if err := options.Validate(); err != nil {
		return err
	}
	transport, err := padui.ParseKittyTransport(*kittyTransport)
	if err != nil {
		return fmt.Errorf("--kitty-transport: %w", err)
	}

	app, err := appinit.Bootstrap(flags, appinit.WithoutEngine(noEngine), appinit.AllowModelSelection())
	if err != nil {
		return err
	}
	defer func() { app.Close(err) }()

	m := newModel(app)
	m.setKittyTransport(transport)
	m.runOptions = *options
	m.maxRounds = options.ToolRounds
	p := tea.NewProgram(m)
	final, runErr := p.Run()

	// Recover what the model owns from its final state.
	if fm, ok := final.(model); ok {
		fm.shutdown()
	}
	return runErr
}

// Exported helpers for harness users / headless embedding.
func NewEmptyWorld() *world.World { return world.NewWorld() }
