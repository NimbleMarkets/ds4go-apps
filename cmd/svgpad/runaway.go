package main

import (
	"errors"
	"strings"
)

const (
	// svgTurnMaxTokens is the generation budget for a normal build turn —
	// large enough for a detailed SVG drafted across svg_append calls.
	svgTurnMaxTokens = 8192

	// svgAutoCorrectMaxTokens caps each auto-correction retry. A degenerate
	// retry (e.g. a heavily quantized model rambling without producing SVG)
	// then costs ~1 min instead of ~5, and three retries cannot fill the
	// session context — so the auto-correct loop reaches its clean
	// maxAutoCorrect stop instead of dying on "context full".
	svgAutoCorrectMaxTokens = 2048

	// runawayMarkerLimit is how many tool-call open markers a single
	// generation round may emit before it is judged a runaway loop. A
	// healthy round emits one (or a few parallel) calls and stops at the
	// tool-block close; a degenerate quant loops the open marker hundreds of
	// times to fill the token budget (see svgpad.log:217), so a limit well
	// above any plausible parallel-call count has no false positives.
	runawayMarkerLimit = 12

	// toolMarkerNeedle appears in every tool-call opening the model emits,
	// well-formed (<｜DSML｜invoke name="…">) or garbled (<｜DSinvoke
	// name="…">, <｜DSml_invoke name="…">) — all contain "invoke name=".
	toolMarkerNeedle = "invoke name="
)

// errRunaway marks a turn the runaway guard stopped early. It is distinct
// from context.Canceled (a user abort) so the turn handler can report it
// clearly and skip auto-correction.
var errRunaway = errors.New("generation stopped: model looped on tool-call markers")

// turnMaxTokens returns the generation budget for a turn given how many
// auto-correction retries have already run for the current job.
func turnMaxTokens(autoCorrectCount int) int {
	if autoCorrectCount > 0 {
		return svgAutoCorrectMaxTokens
	}
	return svgTurnMaxTokens
}

// runawayGuard counts tool-call open markers across a streaming generation
// round and reports when the count crosses runawayMarkerLimit, indicating the
// model is looping on tool markers instead of making progress. It is fed raw
// token text (which may split the needle across deltas) and reset at each
// round boundary.
type runawayGuard struct {
	count int
	tail  string // trailing bytes that may begin a needle split across deltas
}

func newRunawayGuard() *runawayGuard { return &runawayGuard{} }

// feed adds a raw token delta and reports whether the runaway limit has been
// crossed. Once tripped it keeps returning true until reset. The retained
// tail (needle length minus one) is too short to hold a whole needle, so
// markers spanning a delta boundary are counted exactly once.
func (g *runawayGuard) feed(delta string) bool {
	seg := g.tail + delta
	g.count += strings.Count(seg, toolMarkerNeedle)
	if carry := len(toolMarkerNeedle) - 1; len(seg) > carry {
		g.tail = seg[len(seg)-carry:]
	} else {
		g.tail = seg
	}
	return g.count >= runawayMarkerLimit
}

// reset clears the guard at a round boundary.
func (g *runawayGuard) reset() {
	g.count = 0
	g.tail = ""
}
