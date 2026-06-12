// luaview.go: syntax-highlighted source panel for the active Lua script.
// 'v' toggles it; it shares the bottom row side-by-side with the LLM Output
// box. During generation it follows the file the model is writing — the
// panel auto-opens on the first lua_* tool call and reloads on every tool
// result — and follows the tail unless scrolled (tab focus, ↑/↓).

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

// sourceVisibleLines is how many source lines the panel window shows —
// sized so the panel matches the LLM Output box's 13-row footprint
// (border + title + content).
const sourceVisibleLines = 9

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

// activeLuaPath returns the script the source panel should show. While a
// generation is running the file the model is writing wins; otherwise the
// pgup/pgdown browsed history entry, then the last active file.
func (m model) activeLuaPath() (name, path string) {
	if m.inferencing && m.lastActiveLua != "" {
		return filepath.Base(m.lastActiveLua), m.lastActiveLua
	}
	if m.luaEntryIndex >= 0 && m.luaEntryIndex < len(m.luaEntries) {
		e := m.luaEntries[m.luaEntryIndex]
		return e.filename, e.path
	}
	if m.lastActiveLua != "" {
		return filepath.Base(m.lastActiveLua), m.lastActiveLua
	}
	return "", ""
}

// reloadSource (re)reads and highlights the active script into the panel.
// Returns false when there is nothing to show.
func (m *model) reloadSource() bool {
	name, path := m.activeLuaPath()
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	src := sanitizeForDisplay(string(data))
	m.sourceName = name
	m.sourceLines = strings.Split(strings.TrimRight(highlightLua(src), "\n"), "\n")
	return true
}

// toggleSourceView opens or closes the source panel, loading the active
// script on open.
func (m *model) toggleSourceView() {
	if m.showSource {
		m.showSource = false
		if m.focus == focusSource {
			m.focus = focusViewport
		}
		return
	}
	if !m.reloadSource() {
		m.status = "no lua script yet — generate something or pgup/pgdown to browse"
		return
	}
	m.sourceScroll = 0 // follow the tail
	m.showSource = true
}

// sourcePanel renders the bordered source panel at the given total width.
func (m model) sourcePanel(w int) string {
	innerW := w - 4 // border + padding
	if innerW < 8 {
		innerW = 8
	}

	total := len(m.sourceLines)
	scroll := m.sourceScroll
	if scroll > total-sourceVisibleLines {
		scroll = total - sourceVisibleLines
	}
	if scroll < 0 {
		scroll = 0
	}
	start := total - sourceVisibleLines - scroll
	if start < 0 {
		start = 0
	}
	end := start + sourceVisibleLines
	if end > total {
		end = total
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		ln := fmt.Sprintf("%s %s", dimStyle.Render(fmt.Sprintf("%3d │", i+1)), m.sourceLines[i])
		b.WriteString(ansi.Truncate(ln, innerW, "…"))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	content := b.String()
	if content == "" {
		content = dimStyle.Render("(empty file)")
	}

	borderColor := "11"
	if m.focus == focusSource {
		borderColor = "3"
	}
	title := "Lua Source (toggle with v) · " + m.sourceName
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Width(w).
		MaxHeight(13).
		Padding(0, 1).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			ansi.Truncate(title, innerW, "…"),
			content,
		))
}
