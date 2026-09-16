package main

import (
	"testing"

	"github.com/spf13/pflag"
)

// Greedy decoding marker-loops on some prompts (experiment F vs G,
// 2026-09-15) and produces byte-identical retries; sampling is the default.
func TestSamplingDefaultsEnableSampling(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	var s samplingOptions
	registerSamplingFlags(fs, &s)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if s.Temp != 0.7 {
		t.Errorf("default --temp = %g, want 0.7", s.Temp)
	}
	if s.TopP != 0.95 {
		t.Errorf("default --top-p = %g, want 0.95", s.TopP)
	}
	if s.Seed != 0 {
		t.Errorf("default --seed = %d, want 0 (0 = fresh random seed per turn)", s.Seed)
	}
}

func TestEffectiveSeedVariesWhenUnpinned(t *testing.T) {
	var s samplingOptions
	if a, b := s.effectiveSeed(), s.effectiveSeed(); a == b {
		t.Error("unpinned seed repeated; regenerate and auto-correct retries would replay the identical failure")
	}
	pinned := samplingOptions{Seed: 7}
	if a, b := pinned.effectiveSeed(), pinned.effectiveSeed(); a != 7 || b != 7 {
		t.Error("pinned seed must be stable across turns")
	}
}

func TestSamplingOptionsValidate(t *testing.T) {
	cases := []struct {
		name    string
		opts    samplingOptions
		wantErr bool
	}{
		{"defaults are greedy and valid", samplingOptions{TopP: 0.95}, false},
		{"typical sampling", samplingOptions{Temp: 0.8, TopP: 0.95, Seed: 42}, false},
		{"negative temp", samplingOptions{Temp: -0.1, TopP: 0.95}, true},
		{"temp too high", samplingOptions{Temp: 2.5, TopP: 0.95}, true},
		{"top-p above 1", samplingOptions{Temp: 0.8, TopP: 1.2}, true},
		{"negative top-p", samplingOptions{Temp: 0.8, TopP: -0.5}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("validate() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
