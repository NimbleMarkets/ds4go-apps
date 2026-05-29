package bubble

import tea "charm.land/bubbletea/v2"

// Wait returns a tea.Cmd that blocks until a value is received on ch,
// then returns that value as a tea.Msg.
//
// This is the standard pattern used across the ds4go TUI apps to bridge
// a background goroutine (usually a generation) back into the Bubble Tea
// update loop.
func Wait(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}
