package main

import (
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
)

func TestSubmitFromDormantOpens(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusDormant}
	s, a := s.onSubmit()
	if a != actionOpen {
		t.Errorf("action = %v, want actionOpen", a)
	}
	if s.status != engineinit.StatusOpening {
		t.Errorf("status = %v, want Opening", s.status)
	}
	if !s.pendingSubmit {
		t.Error("pendingSubmit should be true")
	}
}

func TestSubmitFromReadyGenerates(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusReady}
	s, a := s.onSubmit()
	if a != actionGenerate {
		t.Errorf("action = %v, want actionGenerate", a)
	}
	if s.status != engineinit.StatusReady {
		t.Errorf("status = %v, want Ready", s.status)
	}
	if s.pendingSubmit {
		t.Error("pendingSubmit should remain false when generating immediately")
	}
}

func TestSubmitFromOpeningQueues(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusOpening}
	s, a := s.onSubmit()
	if a != actionNone {
		t.Errorf("action = %v, want actionNone", a)
	}
	if !s.pendingSubmit {
		t.Error("pendingSubmit should be true")
	}
}

func TestSubmitFromErrorRetries(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusError}
	s, a := s.onSubmit()
	if a != actionOpen {
		t.Errorf("action = %v, want actionOpen", a)
	}
	if s.status != engineinit.StatusOpening {
		t.Errorf("status = %v, want Opening", s.status)
	}
}

func TestReleaseFromReadyIdleReleases(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusReady}
	s, a := s.onReleaseRequest(false)
	if a != actionRelease {
		t.Errorf("action = %v, want actionRelease", a)
	}
	if s.status != engineinit.StatusDormant {
		t.Errorf("status = %v, want Dormant", s.status)
	}
}

func TestReleaseFromReadyBusyDefers(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusReady}
	s, a := s.onReleaseRequest(true)
	if a != actionNone {
		t.Errorf("action = %v, want actionNone (deferred)", a)
	}
	if !s.releaseRequested {
		t.Error("releaseRequested should be true")
	}
	if s.status != engineinit.StatusReady {
		t.Errorf("status = %v, want Ready (no transition yet)", s.status)
	}
}

func TestReleaseFromOpeningQueues(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusOpening, pendingSubmit: true}
	s, a := s.onReleaseRequest(false)
	if a != actionNone {
		t.Errorf("action = %v, want actionNone", a)
	}
	if !s.releaseRequested {
		t.Error("releaseRequested should be true")
	}
	if s.pendingSubmit {
		t.Error("pendingSubmit should be cleared (release wins)")
	}
}

func TestReleaseFromDormantNoop(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusDormant}
	s, a := s.onReleaseRequest(false)
	if a != actionNone {
		t.Errorf("action = %v, want actionNone", a)
	}
	if s.releaseRequested {
		t.Error("releaseRequested should remain false")
	}
}

func TestEngineOpenedOK(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusOpening}
	s, a := s.onEngineOpened(true)
	if a != actionNone {
		t.Errorf("action = %v, want actionNone (no queued intent)", a)
	}
	if s.status != engineinit.StatusReady {
		t.Errorf("status = %v, want Ready", s.status)
	}
}

func TestEngineOpenedOKWithPendingSubmit(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusOpening, pendingSubmit: true}
	s, a := s.onEngineOpened(true)
	if a != actionGenerate {
		t.Errorf("action = %v, want actionGenerate", a)
	}
	if s.status != engineinit.StatusReady {
		t.Errorf("status = %v, want Ready", s.status)
	}
	if s.pendingSubmit {
		t.Error("pendingSubmit should be cleared")
	}
}

func TestEngineOpenedOKWithQueuedReleaseWins(t *testing.T) {
	s := engineLifecycle{
		status:           engineinit.StatusOpening,
		pendingSubmit:    true, // both queued
		releaseRequested: true,
	}
	s, a := s.onEngineOpened(true)
	if a != actionRelease {
		t.Errorf("action = %v, want actionRelease (release wins)", a)
	}
	if s.status != engineinit.StatusDormant {
		t.Errorf("status = %v, want Dormant", s.status)
	}
	if s.pendingSubmit || s.releaseRequested {
		t.Error("queued flags should be cleared")
	}
}

func TestEngineOpenedFail(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusOpening, pendingSubmit: true}
	s, a := s.onEngineOpened(false)
	if a != actionNone {
		t.Errorf("action = %v, want actionNone", a)
	}
	if s.status != engineinit.StatusError {
		t.Errorf("status = %v, want Error", s.status)
	}
	if !s.pendingSubmit {
		t.Error("pendingSubmit should be preserved for retry")
	}
}

func TestWorkDoneNoReleaseQueued(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusReady}
	s, a := s.onWorkDone(false)
	if a != actionNone {
		t.Errorf("action = %v, want actionNone", a)
	}
	if s.status != engineinit.StatusReady {
		t.Errorf("status = %v, want Ready", s.status)
	}
}

func TestWorkDoneReleaseQueuedAndIdle(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusReady, releaseRequested: true}
	s, a := s.onWorkDone(false)
	if a != actionRelease {
		t.Errorf("action = %v, want actionRelease", a)
	}
	if s.status != engineinit.StatusDormant {
		t.Errorf("status = %v, want Dormant", s.status)
	}
	if s.releaseRequested {
		t.Error("releaseRequested should be cleared")
	}
}

func TestWorkDoneReleaseQueuedButStillBusy(t *testing.T) {
	// generation finished but metadata is still in flight (or vice versa).
	s := engineLifecycle{status: engineinit.StatusReady, releaseRequested: true}
	s, a := s.onWorkDone(true)
	if a != actionNone {
		t.Errorf("action = %v, want actionNone (still busy)", a)
	}
	if !s.releaseRequested {
		t.Error("releaseRequested should remain true")
	}
	if s.status != engineinit.StatusReady {
		t.Errorf("status = %v, want Ready", s.status)
	}
}

func TestEngineReleased(t *testing.T) {
	s := engineLifecycle{status: engineinit.StatusReady}
	s = s.onEngineReleased()
	if s.status != engineinit.StatusDormant {
		t.Errorf("status = %v, want Dormant", s.status)
	}
}

func TestEngineReleasedHandlerLandsInDormant(t *testing.T) {
	// Defensive: the helper used by the engineReleasedMsg handler must
	// land any prior status in Dormant.
	for _, start := range []engineinit.Status{
		engineinit.StatusReady,
		engineinit.StatusError,
		engineinit.StatusOpening,
	} {
		s := engineLifecycle{status: start, releaseRequested: true}
		s = s.onEngineReleased()
		if s.status != engineinit.StatusDormant {
			t.Errorf("from %v: status = %v, want Dormant", start, s.status)
		}
	}
}
