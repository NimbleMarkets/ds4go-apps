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
	done   chan struct{}
}

// Start launches fn in a background goroutine and returns the handle plus
// the initial wait Cmd to return from Update. fn sends TokenMsg/DoneMsg/etc.
// on ch and must honor ctx; it may close ch when finished — the handle never
// closes it. The channel is buffered to absorb UI bursts; content-bearing
// events must use cancellation-aware delivery rather than dropping tokens.
// Callers must keep re-arming Wait until
// they receive the terminal message (e.g. DoneMsg), which keeps the channel draining.
func Start(fn func(ctx context.Context, ch chan<- tea.Msg)) (*Generation, tea.Cmd) {
	ch := make(chan tea.Msg, 64)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(ctx, ch)
	}()
	g := &Generation{ch: ch, ctx: ctx, cancel: cancel, done: done}
	return g, g.Wait()
}

// StopAndWait is for shutdown after the UI stops consuming events. Cancel the
// worker and drain its queued messages before releasing resources it owns.
// The worker must honor its context; terminal messages may still be sent.
func (g *Generation) StopAndWait() {
	if g == nil {
		return
	}
	g.Cancel()
	ch := g.ch
	for {
		select {
		case <-g.done:
			return
		case _, ok := <-ch:
			if !ok {
				ch = nil
			}
		}
	}
}

// Send delivers content without dropping it when the consumer falls behind.
func Send(ctx context.Context, ch chan<- tea.Msg, msg tea.Msg) bool {
	select {
	case ch <- msg:
		return true
	case <-ctx.Done():
		return false
	}
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
