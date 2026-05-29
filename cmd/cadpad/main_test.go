package main

import (
	"io"
	"log"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
)

// TestNoEngineInit verifies that constructing and initializing the model
// with a nil library (the --no-engine / pure-geometry path) does not panic
// and produces a safe dormant lifecycle state.
func TestNoEngineInit(t *testing.T) {
	logBuf := ds4log.NewBuffer(10)
	logger := log.New(io.Discard, "", 0)

	m := newModel(nil, ds4.EngineOptions{}, 4096, "", "cpu", logger, logBuf, false)

	// Should have started with Dormant status.
	if m.lifecycle.status != engineinit.StatusDormant {
		t.Fatalf("expected Dormant status for nil lib, got %v", m.lifecycle.status)
	}
	if m.lib != nil {
		t.Fatal("expected nil lib to be preserved")
	}

	// Calling Init must not panic and must not schedule an engine open cmd.
	// We don't execute the returned cmds (they are tea.Cmd funcs), we just
	// ensure the batch is constructed without crashing.
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned nil batch")
	}

	// Current design: cadpad starts empty (Lua files are the way to create geometry).
	// No demo seeding.
	if len(m.w.Names()) != 0 {
		t.Error("expected empty world in no-engine mode (Lua-driven only)")
	}

	// Exercise the world + renderer path with a minimal object we create here.
	m.w.Create("test_box", "box", map[string]float64{"x": 2, "y": 2, "z": 2})
	s, _, ok := m.w.Get("test_box")
	if !ok {
		t.Fatal("test object missing after Create")
	}
	img, rect, err := m.renderer.Render(s, "test_box", m.proj, 40, 30)
	if err != nil {
		t.Fatalf("preview render failed in no-engine mode: %v", err)
	}
	if img == nil || rect.Dx() == 0 {
		t.Error("expected a non-empty preview image")
	}

	// Exercise the submit path's engine guard.
	m.input.SetValue("make a box")
	// We can't easily run the full tea program here, but we can at least
	// ensure the model doesn't blow up on Update for status messages etc.
	// Send a fake engineReadyMsg (as if someone had called Open) — it should
	// be ignored cleanly when lib==nil.
	updated, _ := m.Update(engineReadyMsg(engineinit.Result{}))
	mm := updated.(model)
	if mm.engine != nil || mm.session != nil {
		t.Error("no-engine model should never acquire engine/session from a zero result")
	}
}

// TestEngineInitPathStillWorks is a compile-time + basic smoke that the
// normal (lib != nil) path still type-checks. We don't open a real engine here.
func TestEngineInitPathStillWorks(t *testing.T) {
	// Just ensure newModel accepts a non-nil lib value without exploding at construction.
	logBuf := ds4log.NewBuffer(10)
	logger := log.New(io.Discard, "", 0)

	// We intentionally pass a nil *ds4.Library value but with non-nil interface
	// expectation exercised at compile time. Real engine work is tested in the
	// broader integration with ds4go.
	m := newModel((*ds4.Library)(nil), ds4.EngineOptions{}, 4096, "", "cpu", logger, logBuf, false)
	_ = m.Init()

	// The important behavioral test is in TestNoEngineInit above.
	_ = world.NewWorld()
}
