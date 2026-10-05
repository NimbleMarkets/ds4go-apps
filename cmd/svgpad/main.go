// Command ds4go-svgpad is an interactive TUI scratchpad for drawing SVG
// images with a locally-run DeepSeek model, rendered live in the terminal.
package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
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
	var toolRounds int
	pflag.IntVar(&toolRounds, "tool-rounds", defaultToolRounds, "maximum tool rounds per drafting/review phase (minimum 1; final answer gets an extra turn)")
	var visual visualOptions
	registerVisualFlags(pflag.CommandLine, &visual)
	var oneShot oneShotOptions
	registerOneShotFlags(pflag.CommandLine, &oneShot)
	var sampling samplingOptions
	registerSamplingFlags(pflag.CommandLine, &sampling)
	pflag.Parse()
	if err := validateToolRounds(toolRounds); err != nil {
		return err
	}
	if err := visual.resolveAndValidate(); err != nil {
		return err
	}
	if err := oneShot.validate(); err != nil {
		return err
	}
	if err := sampling.validate(); err != nil {
		return err
	}

	var bootstrapOptions []appinit.Option
	if visual.Mode == "on" {
		bootstrapOptions = append(bootstrapOptions, appinit.AllowModelSelection())
	}
	app, err := appinit.Bootstrap(flags, bootstrapOptions...)
	if err != nil {
		return err
	}
	defer func() { app.Close(err) }()
	if visual.Mode != "off" {
		app.EngineOpts.VisionPath = visual.EncoderPath
		ds4.ApplyVisionDefaults(&app.EngineOpts)
	}

	m := newModel(app)
	m.maxToolRounds = toolRounds
	m.visual = visual
	m.oneShot = oneShot
	m.sampling = sampling
	var progOpts []tea.ProgramOption
	if oneShot.Enabled() {
		if m.needsVisionModelSelection() {
			return fmt.Errorf("--prompt cannot open the model picker; pass --model/--vision explicitly or use --visual-review off")
		}
		progOpts = append(progOpts, tea.WithoutRenderer(), tea.WithInput(nil))
	}
	p := tea.NewProgram(m, progOpts...)
	final, runErr := p.Run()

	// The model owns the engine/session once Init's goroutine fires; on
	// exit we recover them from the final model state and close in order.
	// The metadata-enrichment goroutines must finish before the session
	// closes underneath them.
	if fm, ok := final.(model); ok {
		if fm.forceQuit {
			app.ForceExit()
		}
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
		if oneShot.Enabled() {
			if fm.oneShotPath != "" {
				fmt.Println(fm.oneShotPath)
			}
			if runErr == nil {
				runErr = fm.oneShotErr
			}
		}
	}
	return runErr
}

func registerVisualFlags(fs *pflag.FlagSet, visual *visualOptions) {
	fs.StringVar(&visual.Mode, "visual-review", "auto", "SVG image review: auto, on (require vision), or off")
	fs.StringVar(&visual.Mode, "vision-review", "auto", "alias for --visual-review")
	fs.IntVar(&visual.MaxPasses, "visual-rounds", 3, "maximum image review passes per request (1..5)")
	fs.StringVar(&visual.EncoderPath, "vision", "", "installed vision encoder alias or GGUF path (default: installed companion)")
}
