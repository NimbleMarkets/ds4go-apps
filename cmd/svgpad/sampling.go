package main

import (
	"fmt"
	"math/rand/v2"

	"github.com/spf13/pflag"
)

// samplingOptions exposes the driver's sampling knobs. Sampling is the
// default: greedy decoding marker-loops on some prompts and makes
// auto-correct retries replay the identical failure (experiments F vs G,
// 2026-09-15). ds4go's tool loop forces greedy inside tool-call markup
// regardless of temperature, so sampling only shapes free content.
type samplingOptions struct {
	Temp float32
	TopP float32
	Seed uint64
}

func registerSamplingFlags(fs *pflag.FlagSet, s *samplingOptions) {
	fs.Float32Var(&s.Temp, "temp", 0.7, "sampling temperature; 0 = greedy decoding")
	fs.Float32Var(&s.TopP, "top-p", 0.95, "nucleus sampling cutoff, applied when --temp > 0")
	fs.Uint64Var(&s.Seed, "seed", 0, "pin the sampler seed for reproducible turns; 0 = fresh random seed per turn")
}

func (s samplingOptions) validate() error {
	if s.Temp < 0 || s.Temp > 2 {
		return fmt.Errorf("--temp must be in [0, 2], got %g", s.Temp)
	}
	if s.TopP < 0 || s.TopP > 1 {
		return fmt.Errorf("--top-p must be in [0, 1], got %g", s.TopP)
	}
	return nil
}

// effectiveSeed returns the seed for one driver run: the pinned --seed, or a
// fresh random seed so regenerating and auto-correct retries explore instead
// of replaying the previous failure.
func (s samplingOptions) effectiveSeed() uint64 {
	if s.Seed != 0 {
		return s.Seed
	}
	return rand.Uint64()
}
