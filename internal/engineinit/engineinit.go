// Package engineinit owns the GPU/engine readiness lifecycle shared by
// glyphpad and svgpad: Open performs the (slow) ds4 engine + session
// creation off the TUI thread, and Badge renders the small status pill
// shown at the right edge of the header bar.
package engineinit

import (
	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
)

// badgeCellWidth is the visible width every badge is padded to. Keeping
// the cell count constant across status transitions prevents a header
// width mismatch — even if a terminal renders the emoji at a slightly
// different cell width than lipgloss measures, that disagreement is the
// same in every state, so init→ready→error transitions never reflow.
const badgeCellWidth = 9

// Pill styling for the header right edge — yellow while initializing,
// green when ready, red on error. The header background underneath is
// overpainted by the badge's own background where the pill renders.
var (
	initBadgeStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("214")).
			Foreground(lipgloss.Color("16")).
			Bold(true)
	dormantBadgeStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("240")).
			Foreground(lipgloss.Color("231")).
			Bold(true)
	readyBadgeStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("28")).
			Foreground(lipgloss.Color("231")).
			Bold(true)
	errorBadgeStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("160")).
			Foreground(lipgloss.Color("231")).
			Bold(true)
)

// renderPill renders label inside style, padded with trailing spaces to a
// consistent visible width so every badge state takes the same cells.
func renderPill(style lipgloss.Style, label string) string {
	padded := " " + label + " "
	for lipgloss.Width(padded) < badgeCellWidth {
		padded += " "
	}
	return style.Render(padded)
}

// Status is the current GPU/engine state shown by the badge.
type Status int

const (
	StatusInit Status = iota
	StatusDormant
	StatusOpening
	StatusReady
	StatusError
)

// Result is what Open returns. When Err is non-nil, Engine and Session
// are nil; otherwise they are owned by the caller and must be Close()d.
type Result struct {
	Engine   *ds4.Engine
	Session  *ds4.Session
	HasMTP   bool
	MTPDraft int
	Err      error
}

// Open creates the ds4 engine and session synchronously. It is intended
// to be invoked from inside a tea.Cmd so model loading runs in the
// goroutine bubbletea spawns for it. The caller passes the resolved
// library (loaded explicitly so an explicit --lib path is honored — using
// ds4.NewEngine here would re-Load the default path). Both the engine
// and the session are owned by the caller on success and must be
// Close()d at program exit; on failure both are nil and Err carries the
// (enriched) error.
func Open(lib *ds4.Library, opts ds4.EngineOptions, ctxSize int) Result {
	eng, err := lib.NewEngine(opts)
	if err != nil {
		return Result{Err: ds4.EnrichEngineOpenError(err)}
	}
	sess, err := eng.NewSession(ctxSize)
	if err != nil {
		eng.Close()
		return Result{Err: err}
	}
	return Result{
		Engine:   eng,
		Session:  sess,
		HasMTP:   eng.HasMTP(),
		MTPDraft: eng.MTPDraftTokens(),
	}
}

// Badge renders the header right-edge status pill for the given Status.
// Unknown values fall back to the init pill so a future Status addition
// doesn't render an empty pill. All badges share a constant visible
// width (see badgeCellWidth) so transitions never reflow the header.
func Badge(s Status) string {
	switch s {
	case StatusDormant:
		return renderPill(dormantBadgeStyle, "💤 idle")
	case StatusOpening:
		return renderPill(initBadgeStyle, "⏳ open")
	case StatusReady:
		return renderPill(readyBadgeStyle, "⚡ GPU")
	case StatusError:
		return renderPill(errorBadgeStyle, "✗ GPU")
	default:
		return renderPill(initBadgeStyle, "⏳ init")
	}
}
