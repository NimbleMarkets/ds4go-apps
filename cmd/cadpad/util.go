package main

import (
	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
)

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func spinnerTick() tea.Cmd {
	return bubble.SpinnerTick(bubble.DefaultSpinnerInterval) // 180ms, same cadence
}

func thinkModeLabel(mode ds4.ThinkMode) string {
	switch mode {
	case ds4.ThinkHigh:
		return "HIGH"
	case ds4.ThinkMax:
		return "MAX"
	default:
		return "OFF"
	}
}
