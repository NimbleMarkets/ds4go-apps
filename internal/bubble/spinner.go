package bubble

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// SpinnerTick returns a tea.Cmd that will send a SpinnerTickMsg after
// the given interval. Callers are expected to re-arm the ticker on each
// tick while generation is active.
func SpinnerTick(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return SpinnerTickMsg{}
	})
}

// DefaultSpinnerInterval is a reasonable default tick rate for spinners.
const DefaultSpinnerInterval = 180 * time.Millisecond
