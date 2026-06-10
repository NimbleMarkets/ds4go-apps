package bubble

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// recvMsg runs a wait Cmd with a timeout so a regression hangs the test
// for 5 seconds instead of forever.
func recvMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("nil wait cmd")
	}
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	select {
	case m := <-out:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

func TestStartDeliversMessagesInOrder(t *testing.T) {
	g, cmd := Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		ch <- TokenMsg("a")
		ch <- TokenMsg("b")
		ch <- DoneMsg{CtxPos: 7}
	})
	if got := recvMsg(t, cmd); got != TokenMsg("a") {
		t.Errorf("msg 1 = %#v, want TokenMsg(a)", got)
	}
	if got := recvMsg(t, g.Wait()); got != TokenMsg("b") {
		t.Errorf("msg 2 = %#v, want TokenMsg(b)", got)
	}
	done, ok := recvMsg(t, g.Wait()).(DoneMsg)
	if !ok || done.CtxPos != 7 || done.Err != nil {
		t.Errorf("msg 3 = %#v, want DoneMsg{CtxPos: 7}", done)
	}
	// After fn closes the channel, Wait fires a nil msg (bubbletea drops nils).
	if cmd := g.Wait(); cmd == nil || cmd() != nil {
		t.Error("expected non-nil cmd firing nil msg from closed channel")
	}
}

func TestCancelReachesFn(t *testing.T) {
	g, cmd := Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		<-ctx.Done()
		ch <- DoneMsg{Err: ctx.Err()}
	})
	if g.Canceled() {
		t.Error("Canceled() = true before Cancel")
	}
	g.Cancel()
	done, ok := recvMsg(t, cmd).(DoneMsg)
	if !ok || !errors.Is(done.Err, context.Canceled) {
		t.Fatalf("got %#v, want DoneMsg{Err: context.Canceled}", done)
	}
	if !g.Canceled() {
		t.Error("Canceled() = false after Cancel")
	}
	g.Cancel() // repeated Cancel must be safe
}

func TestNilHandleSafety(t *testing.T) {
	var g *Generation
	g.Cancel() // must not panic
	if g.Canceled() {
		t.Error("nil handle Canceled() = true, want false")
	}
	if g.Wait() != nil {
		t.Error("nil handle Wait() should return a nil Cmd")
	}
}
