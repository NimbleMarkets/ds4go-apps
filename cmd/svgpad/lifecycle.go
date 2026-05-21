package main

import "github.com/NimbleMarkets/ds4go-playground/internal/engineinit"

// engineAction is the side-effect a lifecycle transition wants the
// bubbletea Update loop to perform. The state machine is pure; it does
// not dispatch tea.Cmds itself.
type engineAction int

const (
	actionNone     engineAction = iota
	actionOpen                  // fire openEngineCmd
	actionRelease               // fire releaseEngineCmd
	actionGenerate              // start a generation turn from m.input
)

// engineLifecycle captures the engine-side state that decides what
// happens on each lifecycle event. m.generating and m.metadataInFlight
// are passed in as the "busy" flag where relevant; we don't duplicate
// them here.
type engineLifecycle struct {
	status           engineinit.Status
	pendingSubmit    bool // user pressed Enter while not Ready; submit when ready
	releaseRequested bool // user pressed `x` while busy; release when idle
}

// onSubmit handles the user pressing Enter on a prompt. From Ready we
// generate immediately; from any other state we queue the submit and
// (if Dormant or Error) start an open.
func (s engineLifecycle) onSubmit() (engineLifecycle, engineAction) {
	switch s.status {
	case engineinit.StatusReady:
		return s, actionGenerate
	case engineinit.StatusOpening:
		s.pendingSubmit = true
		return s, actionNone
	default: // Dormant, Error, Init
		s.pendingSubmit = true
		s.status = engineinit.StatusOpening
		return s, actionOpen
	}
}

// onReleaseRequest handles the manual release keystroke. busy is true
// when the model has work in flight (generation OR metadata enrichment).
// During Opening we cannot release yet — the open Cmd is owned by a
// goroutine — so we queue the intent and clear any pending submit so
// the resolving engineReadyMsg releases instead of generating.
func (s engineLifecycle) onReleaseRequest(busy bool) (engineLifecycle, engineAction) {
	if s.status != engineinit.StatusReady && s.status != engineinit.StatusOpening {
		return s, actionNone
	}
	if busy || s.status == engineinit.StatusOpening {
		s.releaseRequested = true
		if s.status == engineinit.StatusOpening {
			s.pendingSubmit = false
		}
		return s, actionNone
	}
	s.status = engineinit.StatusDormant
	return s, actionRelease
}

// onEngineOpened resolves an in-flight open. A queued release wins over
// a queued submit because the user's most recent intent was to free the
// lock — generating first would defeat that intent.
func (s engineLifecycle) onEngineOpened(ok bool) (engineLifecycle, engineAction) {
	if !ok {
		s.status = engineinit.StatusError
		// pendingSubmit is preserved so the next Enter retries.
		return s, actionNone
	}
	s.status = engineinit.StatusReady
	if s.releaseRequested {
		s.releaseRequested = false
		s.pendingSubmit = false
		s.status = engineinit.StatusDormant
		return s, actionRelease
	}
	if s.pendingSubmit {
		s.pendingSubmit = false
		return s, actionGenerate
	}
	return s, actionNone
}

// onWorkDone is called when generation finishes OR metadata finishes.
// stillBusy is the "other" busy flag — e.g., when generation ends, pass
// metadataInFlight; when metadata ends, pass generating. Release happens
// only when both are idle.
func (s engineLifecycle) onWorkDone(stillBusy bool) (engineLifecycle, engineAction) {
	if !s.releaseRequested || stillBusy {
		return s, actionNone
	}
	s.releaseRequested = false
	s.status = engineinit.StatusDormant
	return s, actionRelease
}

// onEngineReleased resolves an in-flight release. Always lands in
// Dormant; close errors are logged elsewhere and not state-relevant.
func (s engineLifecycle) onEngineReleased() engineLifecycle {
	s.status = engineinit.StatusDormant
	return s
}
