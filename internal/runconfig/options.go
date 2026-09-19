// Package runconfig shares run settings and budget guidance across pad hosts.
package runconfig

import (
	"fmt"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/spf13/pflag"
	"math"
	"math/rand/v2"
)

type Options struct {
	Temperature, TopP float32
	Seed              uint64
	ToolRounds        int
}

func Register(fs *pflag.FlagSet, toolRounds int) *Options {
	o := &Options{ToolRounds: 20}
	fs.Float32Var(&o.Temperature, "temp", .7, "sampling temperature (0 = greedy)")
	fs.Float32Var(&o.TopP, "top-p", .95, "nucleus sampling cutoff")
	fs.Uint64Var(&o.Seed, "seed", 0, "sampler seed (0 = fresh seed per request)")
	if toolRounds > 0 {
		fs.IntVar(&o.ToolRounds, "tool-rounds", toolRounds, "tool-capable rounds per request; final answer gets an extra turn")
	}
	return o
}
func (o Options) Validate() error {
	if math.IsNaN(float64(o.Temperature)) || o.Temperature < 0 || o.Temperature > 2 {
		return fmt.Errorf("--temp must be between 0 and 2")
	}
	if math.IsNaN(float64(o.TopP)) || o.TopP < 0 || o.TopP > 1 {
		return fmt.Errorf("--top-p must be between 0 and 1")
	}
	if o.ToolRounds < 1 || o.ToolRounds == math.MaxInt {
		return fmt.Errorf("--tool-rounds must be positive and leave room for the final response")
	}
	return nil
}
func (o Options) EffectiveSeed() uint64 {
	if o.Seed != 0 {
		return o.Seed
	}
	return rand.Uint64()
}
func ThinkLabel(t ds4.ThinkMode) string {
	switch t {
	case ds4.ThinkNone:
		return "OFF"
	case ds4.ThinkMax:
		return "MAX"
	default:
		return "HIGH"
	}
}
func CycleThink(t ds4.ThinkMode, delta int) ds4.ThinkMode {
	modes := []ds4.ThinkMode{ds4.ThinkNone, ds4.ThinkHigh, ds4.ThinkMax}
	i := 0
	for n, v := range modes {
		if v == t {
			i = n
		}
	}
	return modes[(i+delta+3)%3]
}
func ContextFeedback(u bubble.ContextUsageEvent) string {
	if u.Capacity <= 0 {
		return ""
	}
	note := fmt.Sprintf("Application context budget: %d of %d tokens used; %d remain. Tool rounds do not reset this capacity.", u.PromptTokens, u.Capacity, u.Remaining())
	if u.Remaining() <= 2048 || float64(u.PromptTokens)/float64(u.Capacity) >= .9 {
		note += " Context critically low: finish now, preserve the artifact, and report unfinished work. Avoid large reads and previews."
	} else if float64(u.PromptTokens)/float64(u.Capacity) >= .75 {
		note += " Stop adding detail; use small reads and finish essential validation."
	}
	return note
}
func PrepareHistory(history []ds4.ChatMessage, round, maxRounds int) []ds4.ChatMessage {
	out := append([]ds4.ChatMessage(nil), history...)
	remaining := max(0, maxRounds-1-round)
	note := fmt.Sprintf("Application tool budget: %d of %d tool-capable rounds remain. A round is one assistant batch and can contain multiple tool calls. Finish the current artifact within this budget.", remaining, maxRounds-1)
	if remaining == 0 {
		note += " No more tools can execute; give a final response and state unfinished work."
	} else if remaining <= 3 {
		note += " Finish essential corrections and validation; do not start new details."
	}
	return append(out, ds4.ChatMessage{Role: "user", ToolCallID: "pad.tool-budget", Content: note})
}
