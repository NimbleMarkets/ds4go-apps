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

// viewportBounds returns the screen rectangle of the viewport panel for
// mouse hit-testing (row 0 is the header).
func (m model) viewportBounds() (x0, y0, x1, y1 int) {
	d := m.bodyDimensions()
	x0 = d.leftW + d.srcW
	return x0, 1, x0 + d.viewW - 1, d.h
}
