// Package headerbar lays out the one-line top header used by glyphpad
// and svgpad: prefix on the left, badge pinned to the right, with status
// and (optional) metrics in between. The layout guarantees an exact
// width — never wraps — by truncating status with an ellipsis or dropping
// metrics when room runs out.
package headerbar

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Layout composes the one-line header bar within exactly width cells.
// Layout: prefix + status + gap + " " + metrics + badge. If metrics + the
// one-cell separator wouldn't leave at least one cell of status, metrics
// is dropped. If status itself doesn't fit, it is truncated to a single
// "…". Width invariant: the returned string is always lipgloss.Width =
// width — no wrapping is possible.
func Layout(width int, prefix, status, metrics, badge string) string {
	pW := lipgloss.Width(prefix)
	bW := lipgloss.Width(badge)
	mW := lipgloss.Width(metrics)

	// Budget for everything between prefix and badge.
	budget := width - pW - bW
	if budget < 0 {
		// Pathologically narrow window — prefix + badge already overflow.
		// Render whatever fits of (prefix + badge), padded/truncated to
		// width. No wrap.
		out := prefix + badge
		return fitToWidth(out, width)
	}

	// Try to fit metrics + a one-cell separator. Require at least 1 cell
	// of status budget to remain so we don't drop status to fit metrics.
	showMetrics := false
	if mW > 0 && mW+1 < budget {
		showMetrics = true
	}
	statusBudget := budget
	if showMetrics {
		statusBudget -= mW + 1
	}

	status = truncateCells(status, statusBudget)
	gap := statusBudget - lipgloss.Width(status)
	if gap < 0 {
		gap = 0
	}

	var sb strings.Builder
	sb.Grow(width)
	sb.WriteString(prefix)
	sb.WriteString(status)
	sb.WriteString(strings.Repeat(" ", gap))
	if showMetrics {
		sb.WriteString(" ")
		sb.WriteString(metrics)
	}
	sb.WriteString(badge)
	return fitToWidth(sb.String(), width)
}

// fitToWidth pads s with trailing spaces, or truncates with an ellipsis,
// so its lipgloss-measured width equals exactly width.
func fitToWidth(s string, width int) string {
	w := lipgloss.Width(s)
	switch {
	case w == width:
		return s
	case w < width:
		return s + strings.Repeat(" ", width-w)
	default:
		return truncateCells(s, width)
	}
}

// truncateCells returns s clipped to at most n visible cells. When clipping
// occurs the result ends in "…" (which counts as one cell). n <= 0 yields
// the empty string; n == 1 yields the lone ellipsis.
func truncateCells(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	// Preserve color sequences and whole graphemes in animated status text.
	return ansi.Truncate(s, n, "…")
}
