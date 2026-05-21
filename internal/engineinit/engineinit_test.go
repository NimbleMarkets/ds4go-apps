package engineinit

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestBadgeInit(t *testing.T) {
	out := Badge(StatusInit)
	for _, want := range []string{"⏳", "init"} {
		if !strings.Contains(out, want) {
			t.Errorf("Badge(StatusInit) = %q, missing %q", out, want)
		}
	}
}

func TestBadgeReady(t *testing.T) {
	out := Badge(StatusReady)
	for _, want := range []string{"⚡", "GPU"} {
		if !strings.Contains(out, want) {
			t.Errorf("Badge(StatusReady) = %q, missing %q", out, want)
		}
	}
}

func TestBadgeError(t *testing.T) {
	out := Badge(StatusError)
	for _, want := range []string{"✗", "GPU"} {
		if !strings.Contains(out, want) {
			t.Errorf("Badge(StatusError) = %q, missing %q", out, want)
		}
	}
}

func TestBadgesShareCellWidth(t *testing.T) {
	// All badges must render at the same visible width so a status
	// transition (init→ready or ready→error) cannot shift the header bar
	// and trigger a width mismatch with the terminal's emoji rendering.
	widths := map[Status]int{
		StatusInit:    lipgloss.Width(Badge(StatusInit)),
		StatusDormant: lipgloss.Width(Badge(StatusDormant)),
		StatusOpening: lipgloss.Width(Badge(StatusOpening)),
		StatusReady:   lipgloss.Width(Badge(StatusReady)),
		StatusError:   lipgloss.Width(Badge(StatusError)),
	}
	first := widths[StatusInit]
	for s, w := range widths {
		if w != first {
			t.Errorf("Badge(%d) width=%d, want %d (all badges must share a width)", s, w, first)
		}
	}
}

func TestBadgeUnknownFallsBackToInit(t *testing.T) {
	// Defensive: a future Status value should not render an empty badge.
	out := Badge(Status(99))
	if !strings.Contains(out, "init") {
		t.Errorf("Badge(unknown) = %q, want init fallback", out)
	}
}

func TestBadgeDormant(t *testing.T) {
	out := Badge(StatusDormant)
	for _, want := range []string{"💤", "idle"} {
		if !strings.Contains(out, want) {
			t.Errorf("Badge(StatusDormant) = %q, missing %q", out, want)
		}
	}
}

func TestBadgeOpening(t *testing.T) {
	out := Badge(StatusOpening)
	for _, want := range []string{"⏳", "open"} {
		if !strings.Contains(out, want) {
			t.Errorf("Badge(StatusOpening) = %q, missing %q", out, want)
		}
	}
}
