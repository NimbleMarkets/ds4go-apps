package main

import (
	"testing"
	"time"
)

// Frames must not wait for the next animation tick once the terminal has
// presented the previous one; otherwise any frame longer than one tick period
// snaps the rate to a fraction of the target (60 -> 30 -> 20 fps).
func TestNextFrameStartsWhenPresentationReleasesSlot(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 32
	m.playing = true
	m.rendering = true
	m.lastTick = time.Now().Add(-time.Second)
	m.frameStart = time.Now().Add(-time.Second)
	_, cmd := m.Update(presentedMsg{})
	if !m.rendering || cmd == nil {
		t.Fatalf("presentation did not start the next frame: rendering=%v cmd=%v", m.rendering, cmd != nil)
	}
	if m.t <= 0 {
		t.Fatal("animation clock did not advance at frame start")
	}
}

func TestNextFrameWaitsForFramePeriod(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 32
	m.playing = true
	m.targetFPS = 5
	m.rendering = true
	m.frameStart = time.Now()
	_, cmd := m.Update(presentedMsg{})
	if m.rendering {
		t.Fatal("started the next frame before the frame period elapsed")
	}
	if cmd == nil {
		t.Fatal("no deferred frame scheduled")
	}
	m.frameStart = time.Now().Add(-time.Second)
	m.Update(frameDueMsg{})
	if !m.rendering {
		t.Fatal("deferred frame did not start once the period elapsed")
	}
}

func TestPausedPresentationDoesNotStartFrame(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 32
	m.playing = false
	m.rendering = true
	m.frameStart = time.Now().Add(-time.Second)
	m.Update(presentedMsg{})
	if m.rendering {
		t.Fatal("paused animation started a frame on presentation")
	}
}
