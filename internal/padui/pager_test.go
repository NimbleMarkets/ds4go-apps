package padui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestPagerNavigationAndResize(t *testing.T) {
	top := PagerKey("b", -1, 100, 20)
	if top != 60 || PagerKey("j", top, 101, 20) != 61 {
		t.Fatal("paging away from follow lost position")
	}
	for _, key := range []string{"space", "pgdown", "ctrl+d", "j"} {
		if got := PagerKey(key, 999, 8, 20); got != 0 {
			t.Fatalf("%s failed to clamp after resize: %d", key, got)
		}
	}
	if PagerKey("g", 50, 100, 20) != 0 || PagerKey("G", 0, 100, 20) != -1 || PagerKey("ctrl+u", 40, 100, 20) != 30 {
		t.Fatal("start/end/half page navigation failed")
	}
	for _, size := range [][2]int{{1, 1}, {20, 8}, {80, 24}, {180, 50}} {
		w, h := PagerSize(size[0], size[1])
		lines := make([]string, 100)
		for i := range lines {
			lines[i] = fmt.Sprintf("%03d %s", i+1, strings.Repeat("x", max(0, w-4)))
		}
		view := Pager(size[0], size[1], strings.Repeat("Long title ", 50), lines, -1, "Space/b page · Esc close")
		if lipgloss.Width(view) != size[0] || lipgloss.Height(view) != size[1] {
			t.Fatalf("%v: got %dx%d", size, lipgloss.Width(view), lipgloss.Height(view))
		}
		if size[0] >= 80 && !strings.Contains(ansi.Strip(view), fmt.Sprintf("Lines %d–100 of 100 · following", 101-h)) {
			t.Fatalf("missing visible range / follow indicator for %v: %s", size, ansi.Strip(view))
		}
	}
}

func TestWGSLHighlightPreservesTextAndMultilineComments(t *testing.T) {
	src := "/* color\ncomment */\nlet p: vec2<f32> = vec2<f32>(0.1);\nreturn vec3<f32>(p.x);"
	got := Highlight(src, "wgsl")
	if ansi.Strip(got) != src || !strings.Contains(got, "\x1b[") {
		t.Fatalf("highlighting changed source or omitted colors: %q", got)
	}
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[1], "\x1b[") {
		t.Fatal("paging into multiline comment loses color")
	}
}
