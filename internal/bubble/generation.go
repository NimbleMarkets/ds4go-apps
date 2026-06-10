package bubble

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// Generation is an in-flight background generation owned by a Bubble Tea
// model. Start wires a cancellable context into the generate function, so
// Cancel reliably stops a run that honors ctx (via GenerateOptions.Context).
type Generation struct {
	ch     chan tea.Msg
	ctx    context.Context
	cancel context.CancelFunc
}

// Start launches fn in a background goroutine and returns the handle plus
// the initial wait Cmd to return from Update. fn sends TokenMsg/DoneMsg/etc.
// on ch and must honor ctx; it may close ch when finished — the handle never
// closes it. The channel is buffered so token senders can drop
// non-blockingly when the UI falls behind.
func Start(fn func(ctx context.Context, ch chan<- tea.Msg)) (*Generation, tea.Cmd) {
	ch := make(chan tea.Msg, 64)
	ctx, cancel := context.WithCancel(context.Background())
	go fn(ctx, ch)
	g := &Generation{ch: ch, ctx: ctx, cancel: cancel}
	return g, g.Wait()
}

// Wait returns a Cmd that delivers the next message from the generation;
// return it from Update after each received message to keep streaming.
// Safe on a nil handle (returns a nil Cmd).
func (g *Generation) Wait() tea.Cmd {
	if g == nil {
		return nil
	}
	return Wait(g.ch)
}

// Cancel cancels the context fn received. Safe on a nil handle and safe to
// call repeatedly.
func (g *Generation) Cancel() {
	if g != nil && g.cancel != nil {
		g.cancel()
	}
}

// Canceled reports whether the generation's context has been canceled.
// Safe on a nil handle.
func (g *Generation) Canceled() bool {
	return g != nil && g.ctx != nil && g.ctx.Err() == context.Canceled
}
