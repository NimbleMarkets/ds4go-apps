package main

import (
	"strings"
	"testing"
)

// TestTurnMaxTokensInitialTurnFull verifies the first build turn keeps the
// full generation budget — a detailed SVG drafted across svg_append calls
// legitimately needs it.
func TestTurnMaxTokensInitialTurnFull(t *testing.T) {
	if got := turnMaxTokens(0); got != svgTurnMaxTokens {
		t.Errorf("turnMaxTokens(0) = %d, want %d", got, svgTurnMaxTokens)
	}
}

// TestTurnMaxTokensAutoCorrectReduced verifies auto-correction retries run on
// a smaller budget so a degenerate retry costs ~1 min not ~5 and three of
// them cannot fill the session context.
func TestTurnMaxTokensAutoCorrectReduced(t *testing.T) {
	if svgAutoCorrectMaxTokens >= svgTurnMaxTokens {
		t.Fatalf("auto-correct budget %d must be smaller than turn budget %d",
			svgAutoCorrectMaxTokens, svgTurnMaxTokens)
	}
	for _, n := range []int{1, 2, 3} {
		if got := turnMaxTokens(n); got != svgAutoCorrectMaxTokens {
			t.Errorf("turnMaxTokens(%d) = %d, want %d", n, got, svgAutoCorrectMaxTokens)
		}
	}
}

// TestRunawayGuardIgnoresSingleToolCall: one well-formed tool call is normal,
// never a runaway.
func TestRunawayGuardIgnoresSingleToolCall(t *testing.T) {
	g := newRunawayGuard()
	stream := "I'll draw it.\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke name=\"svg_append\">\n" +
		"<｜DSML｜parameter name=\"chunk\" string=\"true\"><svg/></｜DSML｜parameter>\n" +
		"</｜DSML｜invoke>\n</｜DSML｜tool_calls>"
	if g.feed(stream) {
		t.Fatal("single tool call tripped the runaway guard")
	}
}

// TestRunawayGuardIgnoresLegitParallelCalls: a handful of parallel calls in
// one block is still legitimate.
func TestRunawayGuardIgnoresLegitParallelCalls(t *testing.T) {
	g := newRunawayGuard()
	var b strings.Builder
	for i := 0; i < 3; i++ {
		b.WriteString("<｜DSML｜invoke name=\"svg_append\">...</｜DSML｜invoke>\n")
	}
	if g.feed(b.String()) {
		t.Fatal("3 parallel calls tripped the runaway guard")
	}
}

// TestRunawayGuardTripsOnMarkerLoop reproduces svgpad.log:217 — the model
// loops emitting bare invoke openings to fill the token budget.
func TestRunawayGuardTripsOnMarkerLoop(t *testing.T) {
	g := newRunawayGuard()
	line := "<｜DSinvoke name=\"svg_append\">\n"
	tripped := false
	for i := 0; i < runawayMarkerLimit+5 && !tripped; i++ {
		tripped = g.feed(line)
	}
	if !tripped {
		t.Fatalf("marker loop did not trip the guard within %d markers", runawayMarkerLimit+5)
	}
}

// TestRunawayGuardCountsAcrossDeltaBoundaries: tokens routinely split the
// marker across deltas; the guard must still count it.
func TestRunawayGuardCountsAcrossDeltaBoundaries(t *testing.T) {
	g := newRunawayGuard()
	tripped := false
	for i := 0; i < (runawayMarkerLimit+2)*2 && !tripped; i++ {
		g.feed("...invoke na")
		if g.feed("me=\"svg_append\"...") {
			tripped = true
		}
	}
	if !tripped {
		t.Fatal("guard failed to count markers split across deltas")
	}
}

// TestRunawayGuardResetClearsCount: a round boundary resets accumulated count
// so legitimate markers across rounds do not add up to a false trip.
func TestRunawayGuardResetClearsCount(t *testing.T) {
	g := newRunawayGuard()
	line := "<｜DSinvoke name=\"svg_append\">\n"
	for i := 0; i < runawayMarkerLimit-1; i++ {
		g.feed(line)
	}
	g.reset()
	if g.feed(line) {
		t.Fatal("guard tripped after reset with a single marker")
	}
}

// TestRunawayGuardIgnoresRepeatedSVGContent: a Mandelbrot-style draft is
// thousands of near-identical <rect> lines inside one svg_append chunk — that
// repetition must NOT look like a runaway (regression against a naive
// line-repetition heuristic).
func TestRunawayGuardIgnoresRepeatedSVGContent(t *testing.T) {
	g := newRunawayGuard()
	var b strings.Builder
	b.WriteString("<｜DSML｜invoke name=\"svg_append\">")
	for i := 0; i < 5000; i++ {
		b.WriteString("<rect x=\"1\" y=\"1\" width=\"1\" height=\"1\" fill=\"#abc\"/>\n")
	}
	if g.feed(b.String()) {
		t.Fatal("repeated <rect> content tripped the runaway guard")
	}
}
