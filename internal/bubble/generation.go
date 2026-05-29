package bubble

import (
	"context"

	ds4 "github.com/NimbleMarkets/ds4go"
	tea "charm.land/bubbletea/v2"
)

// Generation represents an in-flight ds4go generation running in the
// background. It is designed to be driven from a Bubble Tea model.
type Generation struct {
	ch      chan tea.Msg
	cancel  context.CancelFunc
	session *ds4.Session
}

// StartGeneration launches a generation (or continuation) in a background
// goroutine and returns a Generation handle plus the initial tea.Cmd that
// should be returned from Update.
//
// The provided generateFn is responsible for doing the actual work
// (building prompts, calling GenerateTokens / ToolLoop, etc.) and
// sending messages on the channel it receives.
//
// Typical usage:
//
//	gen, cmd := bubble.StartGeneration(func(ch chan tea.Msg) {
//	    defer close(ch)
//	    // ... do generation, send TokenMsg, DoneMsg, etc.
//	})
//	return m, tea.Batch(cmd, otherCmds...)
func StartGeneration(generateFn func(ch chan tea.Msg)) (*Generation, tea.Cmd) {
	ch := make(chan tea.Msg, 64)

	_, cancel := context.WithCancel(context.Background())
	// Note: callers are responsible for wiring a context into their
	// GenerateOptions if they want cancellation support.

	go func() {
		defer cancel()
		generateFn(ch)
	}()

	return &Generation{
		ch:     ch,
		cancel: cancel,
	}, Wait(ch)
}

// Cancel requests cancellation of the generation. It is safe to call
// multiple times.
func (g *Generation) Cancel() {
	if g.cancel != nil {
		g.cancel()
	}
}

// Close cleans up the generation. Callers should usually call Cancel
// first if they want to stop an in-progress generation.
func (g *Generation) Close() {
	g.Cancel()
	if g.ch != nil {
		close(g.ch)
	}
}
