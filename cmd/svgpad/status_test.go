package main

import (
	"testing"
)

// TestRoundStatusGenerate verifies the normal tool-round status line.
func TestRoundStatusGenerate(t *testing.T) {
	m := model{toolRounds: 2, maxToolRounds: 10}
	if got, want := m.roundStatus(), "Generate [2/10]..."; got != want {
		t.Errorf("roundStatus() = %q, want %q", got, want)
	}
}

// TestRoundStatusAutoCorrect verifies an auto-correct retry is labeled as
// such instead of masquerading as a fresh generation. Regression test:
// the retry resets the per-segment tool-round counter, so the old
// unconditional "Generate [%d/%d]" rendered as a confusing
// "Generate [0/10]" minutes into a job.
func TestRoundStatusAutoCorrect(t *testing.T) {
	m := model{
		toolRounds: 0, maxToolRounds: 10,
		autoCorrectCount: 1, maxAutoCorrect: 3,
	}
	if got, want := m.roundStatus(), "Fixing SVG (1/3) [0/10]..."; got != want {
		t.Errorf("roundStatus() = %q, want %q", got, want)
	}
}
