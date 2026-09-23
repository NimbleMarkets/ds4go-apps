package padui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
)

// PagerSize uses the terminal's width, with the same border and padding as Dialog.
func PagerSize(width, height int) (int, int) {
	return max(1, width-4), max(1, height-6)
}

// PagerKey navigates wrapped display lines. A negative top follows new output.
func PagerKey(key string, top, total, page int) int {
	page = max(1, page)
	last := max(0, total-page)
	pos := min(max(0, top), last)
	if top < 0 {
		pos = last
	}
	switch key {
	case "up", "k":
		return max(0, pos-1)
	case "down", "j":
		return min(last, pos+1)
	case "pgup", "b":
		return max(0, pos-page)
	case "pgdown", "space", " ":
		return min(last, pos+page)
	case "ctrl+u":
		return max(0, pos-max(1, page/2))
	case "ctrl+d":
		return min(last, pos+max(1, page/2))
	case "home", "g":
		return 0
	case "end", "G", "shift+g":
		return -1
	}
	return top
}

// Pager receives already wrapped, optionally highlighted lines. It never wraps
// them again, so the visible range and keyboard page size agree.
func Pager(width, height int, title string, lines []string, top int, hint string) string {
	w, h := PagerSize(width, height)
	follow := top < 0
	if follow {
		top = len(lines)
	}
	top = min(max(0, top), max(0, len(lines)-h))
	end := min(len(lines), top+h)
	first := min(top+1, end)
	status := fmt.Sprintf("Lines %d–%d of %d", first, end, len(lines))
	if follow {
		status += " · following"
	} else if end < len(lines) {
		status += " · more below"
	}
	body := make([]string, h)
	for i, line := range lines[top:end] {
		body[i] = ansi.Truncate(line, w, "")
	}
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	content := accent.Bold(true).Render(ansi.Truncate(title, w, "…")) + "\n" + strings.Join(body, "\n") + "\n" +
		accent.Render(ansi.Truncate(status, w, "…")) + "\n" + ansi.Truncate(hint, w, "…")
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("75")).Padding(0, 1).Width(w + 4).Render(content)
	// Tiny terminals still need a bounded view so resizing a modal cannot overflow.
	rows := strings.Split(box, "\n")
	for i := range rows {
		rows[i] = ansi.Truncate(rows[i], max(1, width), "")
	}
	box = strings.Join(rows[:min(len(rows), max(1, height))], "\n")
	return lipgloss.Place(max(1, width), max(1, height), lipgloss.Center, lipgloss.Center, box)
}

// Highlight uses the same Chroma palette as cadpad's source pane. Callers must
// sanitize untrusted terminal controls first and keep raw source for copying.
func Highlight(source, language string) string {
	lexer, style, formatter := lexers.Get(language), styles.Get("monokai"), formatters.Get("terminal256")
	if lexer == nil || style == nil || formatter == nil {
		return source
	}
	it, err := lexer.Tokenise(nil, source)
	if err != nil {
		return source
	}
	var b strings.Builder
	for _, line := range chroma.SplitTokensIntoLines(it.Tokens()) {
		if err := formatter.Format(&b, style, chroma.Literator(line...)); err != nil {
			return source
		}
	}
	return b.String()
}
