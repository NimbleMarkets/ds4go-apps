package headerbar

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestLayoutHeaderFitsExactly(t *testing.T) {
	got := Layout(40, "[pre] ", "status", " m ", "[badge]")
	if w := lipgloss.Width(got); w != 40 {
		t.Errorf("width=%d, want 40 — got %q", w, got)
	}
	for _, want := range []string{"[pre]", "status", " m ", "[badge]"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q missing %q", got, want)
		}
	}
}

func TestLayoutHeaderDropsMetricsWhenNoRoom(t *testing.T) {
	// Width = prefix(10) + status(20) + badge(10) = 40. No room for 30-cell
	// metrics; the layout must drop them rather than overflow.
	prefix, status, metrics, badge := "[prefixxx]", "this status here----", "metricsxxxxxxxxxxxxxxxxxxxxxxx", "[badgexxx]"
	got := Layout(40, prefix, status, metrics, badge)
	if w := lipgloss.Width(got); w != 40 {
		t.Errorf("width=%d, want 40 — got %q", w, got)
	}
	if strings.Contains(got, "metrics") {
		t.Errorf("metrics should have been dropped, got %q", got)
	}
}

func TestLayoutHeaderTruncatesStatus(t *testing.T) {
	// Status is too long even with metrics dropped — must truncate.
	got := Layout(20, "[p] ", strings.Repeat("x", 50), "m", "[b]")
	if w := lipgloss.Width(got); w != 20 {
		t.Errorf("width=%d, want 20 — got %q", w, got)
	}
	if !strings.HasPrefix(got, "[p] ") {
		t.Errorf("prefix lost: %q", got)
	}
	if !strings.HasSuffix(got, "[b]") {
		t.Errorf("badge lost: %q", got)
	}
}

func TestLayoutHeaderNarrowWindow(t *testing.T) {
	// Pathologically narrow: prefix + badge alone overflow. Result must
	// still be exactly width cells wide (clamping/truncating as needed),
	// not wrap.
	got := Layout(10, "[long-prefix] ", "status", "m", "[badge]")
	if w := lipgloss.Width(got); w != 10 {
		t.Errorf("width=%d, want 10 — got %q", w, got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("layoutHeader produced multiple lines: %q", got)
	}
}

func TestTruncateCells(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"hello", 1, "…"},
		{"hello", 0, ""},
		{"abc", 3, "abc"},
	} {
		if got := truncateCells(c.s, c.n); got != c.want {
			t.Errorf("truncateCells(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestTruncateCellsCellAware(t *testing.T) {
	// Emoji are 2 cells. Truncating "abc🦤def" to 5 cells should land
	// before the 🦤 (which would push to 6) and emit "abc…".
	got := truncateCells("abc🦤def", 5)
	if lipgloss.Width(got) > 5 {
		t.Errorf("truncateCells produced %q (width %d), want <=5", got, lipgloss.Width(got))
	}
}
