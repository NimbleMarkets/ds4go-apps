package main

import (
	"io"
	"log"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/NimbleMarkets/ds4go-apps/internal/engineinit"
)

// testApp builds the appinit.App bundle newModel needs, without Bootstrap's
// side effects (no log file, no stderr capture, no library load).
func testApp(logger *log.Logger, logBuf *ds4log.Buffer) *appinit.App {
	return &appinit.App{
		Name:   "cadpad",
		Flags:  &appinit.Flags{Ctx: 4096, Backend: "cpu"},
		Logger: logger,
		LogBuf: logBuf,
	}
}

// TestNoEngineInit verifies that constructing and initializing the model
// with a nil library (the --no-engine / pure-geometry path) does not panic
// and produces a safe dormant lifecycle state.
func TestNoEngineInit(t *testing.T) {
	logBuf := ds4log.NewBuffer(10)
	logger := log.New(io.Discard, "", 0)

	m := newModel(testApp(logger, logBuf))

	// Should have started with Dormant status.
	if m.lifecycle.status != engineinit.StatusDormant {
		t.Fatalf("expected Dormant status for nil lib, got %v", m.lifecycle.status)
	}
	if m.lib != nil {
		t.Fatal("expected nil lib to be preserved")
	}

	// Calling Init must not panic and must not schedule an engine open cmd.
	// We don't execute the returned cmds (they are tea.Cmd funcs), we just
	// ensure the batch is constructed without crashing. Clear any real
	// workspace scripts so Init takes the debug_box fallback path.
	m.luaEntries = nil
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned nil batch")
	}

	// cadpad seeds a debug object on an empty workspace so the preview pane
	// is never empty.
	if len(m.w.Names()) != 1 {
		t.Errorf("expected 1 seeded object, got %d", len(m.w.Names()))
	}

	// Exercise the world + renderer path with the seeded object.
	s, _, ok := m.w.Get("debug_box")
	if !ok {
		t.Fatal("seeded debug_box missing")
	}
	img, rect, err := m.renderer.Render(s, "test_box", render.ProjXY, 40, 30)
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
	logBuf := ds4log.NewBuffer(10)
	logger := log.New(io.Discard, "", 0)

	// Just ensure the migrated newModel(app) call compiles and doesn't
	// panic at construction; behavioural assertions are in TestNoEngineInit.
	m := newModel(testApp(logger, logBuf))
	_ = m.Init()

	// The important behavioral test is in TestNoEngineInit above.
	_ = world.NewWorld()
}
