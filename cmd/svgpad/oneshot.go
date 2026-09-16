package main

import (
	"errors"

	"github.com/spf13/pflag"
)

// oneShotOptions is the headless mode: submit --prompt on startup, run the
// normal generate/auto-correct/visual-review pipeline with no renderer, save,
// and quit. --outfile overrides the timestamped save name.
type oneShotOptions struct {
	Prompt  string
	Outfile string
}

// oneShotStartMsg fires from Init to submit the one-shot prompt, standing in
// for the user's Enter keypress.
type oneShotStartMsg struct{}

func registerOneShotFlags(fs *pflag.FlagSet, o *oneShotOptions) {
	fs.StringVar(&o.Prompt, "prompt", "", "run headless: generate this prompt, save the SVG, print its path, and exit")
	fs.StringVar(&o.Outfile, "outfile", "", "with --prompt, save the SVG to this path instead of svgpad.<timestamp>.svg")
}

func (o oneShotOptions) Enabled() bool { return o.Prompt != "" }

func (o oneShotOptions) validate() error {
	if o.Outfile != "" && o.Prompt == "" {
		return errors.New("--outfile requires --prompt")
	}
	return nil
}
