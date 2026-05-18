package main

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ntcharts/v2/canvas"
	"github.com/NimbleMarkets/ntcharts/v2/canvas/graph"
)

// drawCommands applies a list of draw commands to the canvas and returns how
// many drew at least one on-canvas cell. Each line is one command; malformed,
// unknown, or out-of-bounds commands are silently skipped:
//
//	color  NAME            set the pen color for the commands that follow
//	clear                  erase everything drawn so far
//	poke   X Y G           place a single glyph G at (X,Y)
//	htext  X Y TEXT        write TEXT left-to-right from (X,Y)
//	vtext  X Y TEXT        write TEXT top-to-bottom from (X,Y)
//	line   X1 Y1 X2 Y2 G   line of glyph G between two points
//	box    X Y W H         box-drawing frame, top-left (X,Y), size W x H
//	rect   X Y W H G       rectangle outline of glyph G
//	fill   X Y W H G       fill a W x H area with glyph G
//	circle X Y R G         circle outline of glyph G, center (X,Y) radius R
//	disc   X Y R G         filled circle of glyph G
//	ltext  X1 Y1 X2 Y2 T   text along a line   (NO-OP — not yet implemented)
//	ctext  X Y R T         text around a circle (NO-OP — not yet implemented)
func drawCommands(c *canvas.Model, lines []string) int {
	applied := 0
	style := lipgloss.NewStyle()
	for _, raw := range lines {
		op, rest := cutField(stripComment(raw))
		switch strings.ToLower(op) {

		case "color":
			name, _ := cutField(rest)
			name = strings.ToLower(strings.Trim(name, `"`))
			switch name {
			case "default", "reset", "none":
				style = lipgloss.NewStyle()
			default:
				if col, ok := parseColor(name); ok {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(col))
				}
			}

		case "clear": // erase everything drawn so far
			c.Clear()

		case "poke", "htext":
			n, r, ok := cutInts(rest, 2)
			g := cleanGlyph(r)
			if !ok || g == "" {
				continue
			}
			if c.SetStringWithStyle(canvas.Point{X: n[0], Y: n[1]}, g, style) {
				applied++
			}

		case "vtext":
			n, r, ok := cutInts(rest, 2)
			g := cleanGlyph(r)
			if !ok || g == "" {
				continue
			}
			drew := false
			for i, ru := range []rune(g) {
				if c.SetRuneWithStyle(canvas.Point{X: n[0], Y: n[1] + i}, ru, style) {
					drew = true
				}
			}
			if drew {
				applied++
			}

		case "line":
			n, r, ok := cutInts(rest, 4)
			g := cleanGlyph(r)
			if !ok || g == "" {
				continue
			}
			pts := graph.GetLinePoints(canvas.Point{X: n[0], Y: n[1]}, canvas.Point{X: n[2], Y: n[3]})
			if drawPoints(c, pts, g, style) {
				applied++
			}

		case "circle":
			n, r, ok := cutInts(rest, 3)
			g := cleanGlyph(r)
			if !ok || g == "" {
				continue
			}
			pts := graph.GetCirclePoints(canvas.Point{X: n[0], Y: n[1]}, n[2])
			if drawPoints(c, pts, g, style) {
				applied++
			}

		case "disc":
			n, r, ok := cutInts(rest, 3)
			g := cleanGlyph(r)
			if !ok || g == "" {
				continue
			}
			pts := graph.GetFullCirclePoints(canvas.Point{X: n[0], Y: n[1]}, n[2])
			if drawPoints(c, pts, g, style) {
				applied++
			}

		case "box":
			n, _, ok := cutInts(rest, 4)
			if !ok {
				continue
			}
			if drawBox(c, n[0], n[1], n[2], n[3], style) {
				applied++
			}

		case "rect":
			n, r, ok := cutInts(rest, 4)
			g := cleanGlyph(r)
			if !ok || g == "" {
				continue
			}
			if drawRect(c, n[0], n[1], n[2], n[3], g, style) {
				applied++
			}

		case "fill":
			n, r, ok := cutInts(rest, 4)
			g := cleanGlyph(r)
			if !ok || g == "" {
				continue
			}
			if drawFill(c, n[0], n[1], n[2], n[3], g, style) {
				applied++
			}

		case "ltext", "ctext":
			// ltext X1 Y1 X2 Y2 TEXT — text along a line.
			// ctext X Y R TEXT       — text around a circle.
			// No-op placeholder pending text-along-path support in ntcharts
			// (see the DrawTextLine/DrawTextCircle feature request). The names
			// are reserved here but intentionally NOT advertised in the system
			// prompt until they actually draw something.
		}
	}
	return applied
}

// stripComment removes an assembly-style "; comment" (semicolon to end of
// line) and any trailing whitespace before it.
func stripComment(s string) string {
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, " \t")
}

// cutField returns the first whitespace-delimited token of s and the remainder
// with the separating whitespace consumed.
func cutField(s string) (token, rest string) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i:], " \t")
}

// cutInts pulls n integers off the front of s and returns them with the
// remainder. ok is false if fewer than n integers are present.
func cutInts(s string, n int) (vals []int, rest string, ok bool) {
	vals = make([]int, n)
	for i := 0; i < n; i++ {
		var tok string
		tok, s = cutField(s)
		v, err := strconv.Atoi(tok)
		if err != nil {
			return nil, "", false
		}
		vals[i] = v
	}
	return vals, s, true
}

// cleanGlyph trims surrounding quotes the model sometimes adds around G/TEXT.
func cleanGlyph(s string) string { return strings.Trim(s, `"`) }

// commandArity reports how many integer arguments op takes and whether it ends
// with a glyph/text argument; nNums is -1 for an unknown op. Used by the
// debugger's syntax colorizer — keep in sync with drawCommands' switch.
func commandArity(op string) (nNums int, hasGlyph bool) {
	switch op {
	case "clear":
		return 0, false
	case "poke", "htext", "vtext":
		return 2, true
	case "circle", "disc", "ctext":
		return 3, true
	case "line", "rect", "fill", "ltext":
		return 4, true
	case "box":
		return 4, false
	}
	return -1, false
}

// drawPoints stamps glyph g at every point; reports whether any landed on-canvas.
func drawPoints(c *canvas.Model, pts []canvas.Point, g string, style lipgloss.Style) bool {
	drew := false
	for _, p := range pts {
		if c.SetStringWithStyle(p, g, style) {
			drew = true
		}
	}
	return drew
}

// drawBox draws a rounded box-drawing frame with top-left (x,y) and size w x h.
func drawBox(c *canvas.Model, x, y, w, h int, style lipgloss.Style) bool {
	if w < 2 || h < 2 {
		return false
	}
	x2, y2 := x+w-1, y+h-1
	drew := false
	set := func(px, py int, r rune) {
		if c.SetRuneWithStyle(canvas.Point{X: px, Y: py}, r, style) {
			drew = true
		}
	}
	set(x, y, '╭')
	set(x2, y, '╮')
	set(x, y2, '╰')
	set(x2, y2, '╯')
	for i := x + 1; i < x2; i++ {
		set(i, y, '─')
		set(i, y2, '─')
	}
	for j := y + 1; j < y2; j++ {
		set(x, j, '│')
		set(x2, j, '│')
	}
	return drew
}

// drawRect draws a rectangle outline of glyph g.
func drawRect(c *canvas.Model, x, y, w, h int, g string, style lipgloss.Style) bool {
	if w < 1 || h < 1 {
		return false
	}
	x2, y2 := x+w-1, y+h-1
	drew := false
	put := func(px, py int) {
		if c.SetStringWithStyle(canvas.Point{X: px, Y: py}, g, style) {
			drew = true
		}
	}
	for i := x; i <= x2; i++ {
		put(i, y)
		put(i, y2)
	}
	for j := y + 1; j < y2; j++ {
		put(x, j)
		put(x2, j)
	}
	return drew
}

// drawFill fills a w x h rectangle with glyph g.
func drawFill(c *canvas.Model, x, y, w, h int, g string, style lipgloss.Style) bool {
	if w < 1 || h < 1 {
		return false
	}
	drew := false
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			if c.SetStringWithStyle(canvas.Point{X: i, Y: j}, g, style) {
				drew = true
			}
		}
	}
	return drew
}

// colorNames maps friendly color names to hex values.
var colorNames = map[string]string{
	"black":     "#000000",
	"white":     "#ffffff",
	"gray":      "#888888",
	"grey":      "#888888",
	"silver":    "#c0c0c0",
	"red":       "#ff5555",
	"crimson":   "#dc143c",
	"maroon":    "#883344",
	"orange":    "#ffb86c",
	"coral":     "#ff7f50",
	"brown":     "#aa6644",
	"gold":      "#ffd700",
	"yellow":    "#f1fa8c",
	"lime":      "#aaff44",
	"green":     "#50fa7b",
	"teal":      "#44ddbb",
	"turquoise": "#40e0d0",
	"cyan":      "#8be9fd",
	"skyblue":   "#88ccff",
	"blue":      "#5599ff",
	"navy":      "#224488",
	"indigo":    "#6633cc",
	"purple":    "#bd93f9",
	"violet":    "#bd93f9",
	"lavender":  "#c8b8f0",
	"magenta":   "#ff79c6",
	"pink":      "#ff92df",
}

// parseColor resolves a color token (name, #hex, or ANSI index) to a string
// accepted by lipgloss.Color.
func parseColor(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", false
	}
	if s[0] == '#' || isAllDigits(s) {
		return s, true
	}
	if hex, ok := colorNames[s]; ok {
		return hex, true
	}
	return "", false
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
