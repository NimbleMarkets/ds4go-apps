// layout.go: stickers/flexbox geometry for the body row. One row, three
// columns: the left column stacks the Objects and Metrics bubbles, the lua
// source panel sits beside them, and the viewport takes the rest. The same
// dimension pass feeds rendering, the raster budget (viewportInnerSize),
// and mouse hit-testing (viewportBounds), so they cannot drift apart.

package main

import (
	"charm.land/lipgloss/v2"
	"github.com/76creates/stickers/flexbox"
)

// Body column ratios (stickers normalizes them per row).
const (
	leftColRatio   = 20
	sourceColRatio = 30
	viewColRatio   = 50
)

// bodyDims holds the computed body-row cell geometry.
type bodyDims struct {
	leftW int
	srcW  int
	viewW int
	h     int
}

// bodyCells builds the body flexbox row. Cells are returned in order:
// left, source (nil when hidden), viewport.
func (m model) bodyCells() (*flexbox.FlexBox, *flexbox.Cell, *flexbox.Cell, *flexbox.Cell) {
	flex := flexbox.New(m.width, m.bodyH())
	row := flex.NewRow()

	left := flexbox.NewCell(leftColRatio, 1).SetID("left")
	var src *flexbox.Cell
	view := flexbox.NewCell(viewColRatio, 1).SetID("viewport")

	cells := []*flexbox.Cell{left}
	if m.showSource {
		src = flexbox.NewCell(sourceColRatio, 1).SetID("source")
		cells = append(cells, src)
	} else {
		view = flexbox.NewCell(viewColRatio+sourceColRatio, 1).SetID("viewport")
	}
	cells = append(cells, view)

	row.AddCells(cells...)
	flex.AddRows([]*flexbox.Row{row})
	return flex, left, src, view
}

// bodyDimensions runs the flexbox dimension pass and returns the cell
// geometry without rendering content.
func (m model) bodyDimensions() bodyDims {
	flex, left, src, view := m.bodyCells()
	left.SetContent("")
	view.SetContent("")
	if src != nil {
		src.SetContent("")
	}
	_ = flex.Render()

	d := bodyDims{
		leftW: left.GetWidth(),
		viewW: view.GetWidth(),
		h:     m.bodyH(),
	}
	if src != nil {
		d.srcW = src.GetWidth()
	}
	return d
}

func (m model) bodyView() string {
	flex, left, src, view := m.bodyCells()
	left.SetContent("")
	view.SetContent("")
	if src != nil {
		src.SetContent("")
	}
	_ = flex.Render()

	h := m.bodyH()
	objH := h / 2
	leftCol := lipgloss.JoinVertical(lipgloss.Left,
		m.objectsBubble(left.GetWidth(), objH),
		m.metricsBubble(left.GetWidth(), h-objH),
	)
	left.SetContent(leftCol)
	if src != nil {
		src.SetContent(m.sourcePanel(src.GetWidth(), h))
	}
	view.SetContent(m.viewportView(view.GetWidth()))

	return flex.Render()
}

// viewportInnerSize returns the column and row count available for the
// picture widget inside the bordered viewport panel.
func (m model) viewportInnerSize() (cols, rows int) {
	d := m.bodyDimensions()
	cols = max(8, d.viewW-2) // subtract border
	rows = max(6, d.h-3)     // subtract border + header line
	return cols, rows
}

// previewPixelSize returns the viewport's size in terminal pixels, using the
// cell size the terminal reported (8x16 until it answers). Rendering for this
// size keeps the preview's aspect equal to the viewport's on any font; the
// renderer's edge caps bound the actual raster.
func (m model) previewPixelSize() (w, h int) {
	cols, rows := m.viewportInnerSize()
	cw, ch := m.pic.CellPixelSize()
	return cols * cw, rows * ch
}

// viewportBounds returns the screen rectangle of the viewport panel for
// mouse hit-testing (row 0 is the header).
func (m model) viewportBounds() (x0, y0, x1, y1 int) {
	d := m.bodyDimensions()
	x0 = d.leftW + d.srcW
	return x0, 1, x0 + d.viewW - 1, d.h
}

func (m model) sourceBounds() (x0, y0, x1, y1 int, ok bool) {
	if !m.showSource {
		return 0, 0, 0, 0, false
	}
	d := m.bodyDimensions()
	if d.srcW <= 0 {
		return 0, 0, 0, 0, false
	}
	x0 = d.leftW
	return x0, 1, x0 + d.srcW - 1, d.h, true
}

func (m model) luaOutputBounds() (x0, y0, x1, y1 int, ok bool) {
	if !m.showLuaOutput {
		return 0, 0, 0, 0, false
	}
	y0 = 1 + m.bodyH()
	if m.showThinking {
		y0 += 14 // thinking box height plus separator line
	}
	return 0, y0, max(0, m.width-1), y0 + 6, true
}
