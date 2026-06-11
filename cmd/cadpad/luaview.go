// luaview.go: scrollable, syntax-highlighted source overlay for the active
// Lua script ('v' in command mode). The active script is the pgup/pgdown
// browsed entry when one exists, falling back to the generation's last
// active file.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
)

// highlightLua renders Lua source with ANSI syntax highlighting. The text
// content is preserved exactly; on any highlighting failure the source is
// returned unhighlighted.
func highlightLua(src string) string {
	lexer := lexers.Get("lua")
	if lexer == nil {
		return src
	}
	style := styles.Get("monokai")
	formatter := formatters.Get("terminal256")
	if style == nil || formatter == nil {
		return src
	}
	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return src
	}
	var b strings.Builder
	if err := formatter.Format(&b, style, it); err != nil {
		return src
	}
	return b.String()
}

// activeLuaPath returns the script the source viewer should show: the
// browsed history entry when one is selected, else the last file the
// generation touched.
func (m model) activeLuaPath() (name, path string) {
	if m.luaEntryIndex >= 0 && m.luaEntryIndex < len(m.luaEntries) {
		e := m.luaEntries[m.luaEntryIndex]
		return e.filename, e.path
	}
	if m.lastActiveLua != "" {
		return filepath.Base(m.lastActiveLua), m.lastActiveLua
	}
	return "", ""
}

// toggleSourceView opens or closes the source overlay, (re)loading and
// highlighting the active script on open.
func (m *model) toggleSourceView() {
	if m.showSource {
		m.showSource = false
		return
	}
	name, path := m.activeLuaPath()
	if path == "" {
		m.status = "no lua script yet — generate something or pgup/pgdown to browse"
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		m.status = "read " + name + ": " + err.Error()
		return
	}
	src := sanitizeForDisplay(string(data))
	m.sourceName = name
	m.sourceLines = strings.Split(strings.TrimRight(highlightLua(src), "\n"), "\n")
	m.sourceTop = 0
	m.showSource = true
}

// sourcePageSize is the per-page scroll distance in the source overlay.
func (m model) sourcePageSize() int {
	h := m.height - 6
	if h < 1 {
		return 1
	}
	return h
}

// sourceScrollBy returns a new sourceTop moved by delta, clamped to the
// content.
func (m model) sourceScrollBy(delta int) int {
	maxTop := len(m.sourceLines) - m.sourcePageSize()
	if maxTop < 0 {
		maxTop = 0
	}
	top := m.sourceTop + delta
	if top < 0 {
		top = 0
	}
	if top > maxTop {
		top = maxTop
	}
	return top
}

// sourceOverlay renders the highlighted script in a centered titled box
// with line numbers; up/down/pgup/pgdown scroll, esc or v closes.
func (m model) sourceOverlay() string {
	innerW := m.width - 6
	if innerW < 10 {
		innerW = 10
	}
	innerH := m.height - 6
	if innerH < 1 {
		innerH = 1
	}

	start := m.sourceTop
	if start > len(m.sourceLines) {
		start = len(m.sourceLines)
	}
	end := start + innerH
	if end > len(m.sourceLines) {
		end = len(m.sourceLines)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		ln := fmt.Sprintf("%s %s", dimStyle.Render(fmt.Sprintf("%3d │", i+1)), m.sourceLines[i])
		b.WriteString(ansi.Truncate(ln, innerW, "…"))
		b.WriteByte('\n')
	}
	hint := fmt.Sprintf("  ↑/↓ · pgup/pgdown scroll · esc/v close   [%d-%d/%d]",
		start+1, end, len(m.sourceLines))
	b.WriteString("\n" + dimStyle.Render(hint))

	box := titledBox(lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		MaxWidth(m.width).
		MaxHeight(m.height).
		Render(b.String()), "lua · "+m.sourceName)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
