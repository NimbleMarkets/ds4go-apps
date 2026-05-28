package steertui

import (
	"strings"
	"testing"
)

func TestLayoutAlignment(t *testing.T) {
	// Mock views
	lanesView := "Lanes:\n- lane1\n- lane2"
	transcriptView := "Transcript:\nThis is line 1\nThis is line 2\nThis is line 3"
	alternativesView := "Alternatives:\n1. Option A\n2. Option B\n3. Option C"
	metricsView := "Metrics:\nTokens: 100\nEntropy: 0.5"
	statusView := "Status Bar | Help"

	width := 100
	height := 40

	promptBox := "Prompt:\nhello world\n"
	rendered := RenderLayout(width, height, FocusTranscript, lanesView, transcriptView, alternativesView, metricsView, "", promptBox, statusView, false)
	lines := strings.Split(rendered, "\n")

	t.Logf("Total lines in rendered layout: %d (expected around %d)", len(lines), height)
	for i, l := range lines {
		t.Logf("Line %02d [len=%d]: %q", i+1, len(l), l)
	}
}

func TestGetLayoutDimensionsLogsOff(t *testing.T) {
	width := 120
	height := 40
	statusView := "Status\nHelp"

	wT, hT, wA, hA, wL, hL, wM, hM, wLogs, hLogs := GetLayoutDimensions(width, height, statusView, false)

	if wLogs != 0 || hLogs != 0 {
		t.Errorf("expected wLogs=0,hLogs=0 when showLogs=false, got (%d,%d)", wLogs, hLogs)
	}
	if wT <= 0 || hT <= 0 {
		t.Errorf("transcript dims should be positive, got (%d,%d)", wT, hT)
	}
	if wA <= 0 || hA <= 0 || wL <= 0 || hL <= 0 || wM <= 0 || hM <= 0 {
		t.Errorf("bottom-row dims should be positive, got alts=(%d,%d) lanes=(%d,%d) metrics=(%d,%d)", wA, hA, wL, hL, wM, hM)
	}
	// Transcript spans full width.
	if wT != width {
		t.Errorf("expected transcript width=%d, got %d", width, wT)
	}
	// Bottom-row columns sum to width.
	if wA+wL+wM != width {
		t.Errorf("expected wAlts+wLanes+wMetrics=%d, got %d", width, wA+wL+wM)
	}
}

func TestGetLayoutDimensionsLogsOn(t *testing.T) {
	width := 120
	height := 40
	statusView := "Status\nHelp"

	wT, hT, wA, hA, wL, hL, wM, hM, wLogs, hLogs := GetLayoutDimensions(width, height, statusView, true)

	if wLogs != width {
		t.Errorf("expected logs width=%d, got %d", width, wLogs)
	}
	if hLogs <= 0 {
		t.Errorf("expected hLogs > 0, got %d", hLogs)
	}
	// hLogs should be smaller than hTranscript (25% vs 45%).
	if hLogs >= hT {
		t.Errorf("expected hLogs (%d) < hTranscript (%d)", hLogs, hT)
	}
	// Bottom row should also be smaller than transcript (30% vs 45%).
	bottomH := hA
	if hL > bottomH {
		bottomH = hL
	}
	if hM > bottomH {
		bottomH = hM
	}
	if bottomH >= hT {
		t.Errorf("expected bottom row height (%d) < hTranscript (%d)", bottomH, hT)
	}
	// Rows should approximately sum to gridHeight (within flexbox rounding, ±3).
	statusHeight := 2
	promptHeight := height * 15 / 100
	if promptHeight < 5 {
		promptHeight = 5
	}
	gridHeight := height - 1 - statusHeight - promptHeight
	sum := hT + hLogs + bottomH
	if sum < gridHeight-3 || sum > gridHeight+3 {
		t.Errorf("expected hT+hLogs+bottomH ≈ %d, got %d", gridHeight, sum)
	}
	_ = wT
	_ = wA
	_ = wL
	_ = wM
}
