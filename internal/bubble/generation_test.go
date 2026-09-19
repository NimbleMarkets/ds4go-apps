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

func TestContentDeliveryDoesNotDropWhenBufferFills(t *testing.T) {
	g, cmd := Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		for i := 0; i < 300; i++ {
			if !Send(ctx, ch, i) {
				return
			}
		}
	})
	defer g.StopAndWait()
	for i := 0; i < 300; i++ {
		if got := recvMsg(t, cmd); got != i {
			t.Fatalf("message %d: %v", i, got)
		}
		cmd = g.Wait()
	}
}

func TestStopAndWaitDrainsTerminalMessages(t *testing.T) {
	finished := make(chan struct{})
	g, _ := Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		defer close(finished)
		<-ctx.Done()
		// A worker may have queued terminal/cleanup events after cancellation.
		for i := 0; i < 100; i++ {
			ch <- DoneMsg{}
		}
	})
	stopped := make(chan struct{})
	go func() { g.StopAndWait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not drain worker")
	}
	select {
	case <-finished:
	default:
		t.Fatal("shutdown returned before worker exit")
	}
	g.StopAndWait()
	var empty *Generation
	empty.StopAndWait()
}
