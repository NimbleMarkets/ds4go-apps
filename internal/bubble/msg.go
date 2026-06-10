// Package bubble provides reusable integration helpers for using
// github.com/NimbleMarkets/ds4go together with charm.land/bubbletea/v2.
//
// This package captures generation/TUI plumbing shared by svgpad,
// glyphpad, and cadpad.
package bubble

// TokenMsg carries one token of generated text from the model.
type TokenMsg string

// DoneMsg signals that a generation (or generation round) has completed.
type DoneMsg struct {
	Err    error
	CtxPos int // session token position at completion
}

// ToolRoundMsg indicates that tool calls were executed and another
// generation round should begin.
type ToolRoundMsg struct{}

// SpinnerTickMsg is emitted periodically by SpinnerTick while generation
// is in progress. Receivers typically increment a frame counter and
// re-arm the ticker.
type SpinnerTickMsg struct{}

// InferencingStartMsg can be used to signal the beginning of an inference
// session (useful for showing spinners or "thinking" UI).
type InferencingStartMsg struct{}
