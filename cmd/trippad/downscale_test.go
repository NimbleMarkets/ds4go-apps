package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func sizedModel(t *testing.T) *model {
	t.Helper()
	m := testModel(t)
	m.width, m.height = 160, 58
	return m
}

func expectedStatus(m *model) string {
	cols, rows := m.viewport()
	cw, ch := m.pic.CellPixelSize()
	_, w, h := viewportResolution(cols, rows, cw, ch, m.downscale)
	return fmt.Sprintf("downscale %d · %dx%d", m.downscale, w, h)
}

func TestDownscaleKeysStepAndClamp(t *testing.T) {
	m := sizedModel(t)
	m.dirty = false
	if cmd := m.key(tea.KeyPressMsg{Code: '-', Text: "-"}); m.downscale != 3 || cmd == nil || m.status != expectedStatus(m) {
		t.Fatalf("minus: downscale=%d render=%v status=%q", m.downscale, cmd != nil, m.status)
	}
	m.rendering = false
	m.key(tea.KeyPressMsg{Code: '=', Text: "="})
	if m.downscale != 2 {
		t.Fatalf("= alias: downscale=%d", m.downscale)
	}
	m.key(tea.KeyPressMsg{Code: '+', Text: "+"})
	if m.downscale != 1 {
		t.Fatalf("plus: downscale=%d", m.downscale)
	}
	for i := 0; i < 10; i++ {
		m.key(tea.KeyPressMsg{Code: '-', Text: "-"})
	}
	if m.downscale != 8 || m.status != expectedStatus(m) {
		t.Fatalf("coarse clamp: downscale=%d status=%q", m.downscale, m.status)
	}
	for i := 0; i < 10; i++ {
		m.key(tea.KeyPressMsg{Code: '+', Text: "+"})
	}
	if m.downscale != 1 {
		t.Fatalf("fine clamp: downscale=%d", m.downscale)
	}
}

func TestDownscaleKeysIgnoredWhileTyping(t *testing.T) {
	m := sizedModel(t)
	m.input.Focus()
	m.key(tea.KeyPressMsg{Code: '-', Text: "-"})
	if m.downscale != 2 || !strings.Contains(m.input.Value(), "-") {
		t.Fatalf("typing '-' changed downscale=%d input=%q", m.downscale, m.input.Value())
	}
}

func TestDownscaleCommand(t *testing.T) {
	m := sizedModel(t)
	m.submit("/downscale 3")
	if m.downscale != 3 || m.status != expectedStatus(m) {
		t.Fatalf("set: downscale=%d status=%q", m.downscale, m.status)
	}
	m.submit("/downscale")
	if m.downscale != 3 || m.status != expectedStatus(m) {
		t.Fatalf("report: downscale=%d status=%q", m.downscale, m.status)
	}
	for _, bad := range []string{"/downscale 9", "/downscale 0", "/downscale x"} {
		m.submit(bad)
		if m.downscale != 3 || !strings.Contains(m.status, "1..8") {
			t.Fatalf("%s: downscale=%d status=%q", bad, m.downscale, m.status)
		}
	}
}

func TestHeaderShowsRaster(t *testing.T) {
	m := sizedModel(t)
	if m.renderCmd() == nil {
		t.Fatal("missing render")
	}
	cols, rows := m.viewport()
	cw, ch := m.pic.CellPixelSize()
	_, w, h := viewportResolution(cols, rows, cw, ch, m.downscale)
	if want := fmt.Sprintf("%dx%d", w, h); !strings.Contains(m.View().Content, want) {
		t.Fatalf("header lacks raster %s", want)
	}
}
