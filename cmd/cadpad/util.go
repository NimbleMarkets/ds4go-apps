package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
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
	return tea.Tick(180*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
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
