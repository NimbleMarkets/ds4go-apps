package padui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"fmt"
	"strings"
	"time"
)

type LoadingTick struct{ Epoch uint64 }
type Loading struct {
	Epoch   uint64
	Active  bool
	Frame   int
	Started time.Time
}

func (l *Loading) Start() tea.Cmd {
	l.Epoch++
	l.Active = true
	l.Frame = 0
	l.Started = time.Now()
	return l.tick()
}
func (l Loading) tick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return LoadingTick{Epoch: l.Epoch} })
}
func (l *Loading) Update(msg LoadingTick) tea.Cmd {
	if !l.Active || l.Epoch != msg.Epoch {
		return nil
	}
	l.Frame++
	return l.tick()
}
func (l Loading) View() string {
	p := l.Frame % 16
	if p > 8 {
		p = 16 - p
	}
	track := "[" + strings.Repeat(" ", p) + "🦤🚲" + strings.Repeat(" ", 8-p) + "]"
	return lipgloss.NewStyle().Background(lipgloss.Color("75")).Foreground(lipgloss.Color("0")).Render(track) + fmt.Sprintf(" loading %ds", int(time.Since(l.Started).Seconds()))
}
