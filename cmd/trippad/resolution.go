package main

import (
	"fmt"
	"math"
)

// viewportResolution keeps the source and placement aspect ratios identical,
// and gives every terminal row an integer number of source pixels. Kitty
// virtual placements preserve aspect ratio even when picture uses FitFill.
// Independently flooring scaled cell dimensions can therefore add padding and
// fractional source-row boundaries (e.g. 14x30 / 3 becomes 4x10).
func viewportResolution(cols, rows, cellW, cellH, downscale int) (factor float64, w, h int) {
	cols, rows = max(1, cols), max(1, rows)
	cellW, cellH = max(1, cellW), max(1, cellH)
	g, b := cellW, cellH
	for b != 0 {
		g, b = b, g%b
	}
	wanted := min(1/float64(max(1, downscale)), 1536/float64(cols*cellW), 1024/float64(rows*cellH))
	// Exact reduced cells are integer multiples of cellW/g by cellH/g.
	// Prefer fidelity over the soft raster budget when the smallest such cell
	// is larger than the requested reduction. Coprime dimensions require 1:1.
	units := min(g, max(1, int(math.Floor(wanted*float64(g)))))
	factor = min(1, math.Nextafter(float64(units)/float64(g), math.Inf(1)))
	// Nextafter avoids a floating-point product just below an integer being
	// truncated one pixel smaller by picture's resolution-factor calculation.
	return factor, cols * (cellW / g) * units, rows * (cellH / g) * units
}

// rasterSize is the GPU raster the next frame will use for the current
// viewport, terminal cell size, and downscale.
func (m *model) rasterSize() (w, h int) {
	cols, rows := m.viewport()
	cw, ch := m.pic.CellPixelSize()
	_, w, h = viewportResolution(cols, rows, cw, ch, m.downscale)
	return w, h
}

// setDownscale applies a viewport downscale of 1..8, reporting the resulting
// raster in the status line. Out-of-range values leave the setting unchanged.
func (m *model) setDownscale(n int) {
	if n < 1 || n > 8 {
		m.status = "downscale must be 1..8"
		return
	}
	if n != m.downscale {
		m.downscale = n
		m.dirty = true
	}
	w, h := m.rasterSize()
	m.status = fmt.Sprintf("downscale %d · %dx%d", m.downscale, w, h)
}
