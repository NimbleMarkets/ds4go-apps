package main

import (
	"context"
	"image"
	"math"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
)

type testRenderer struct{}

func (testRenderer) Compile(shader.Source) error { return nil }
func (testRenderer) Render(_ shader.Source, _ [16]float32, _, _ float32, _ uint32, w, h int) (*image.NRGBA, error) {
	return image.NewNRGBA(image.Rect(0, 0, w, h)), nil
}
func testModel(t *testing.T) *model {
	t.Helper()
	s, err := tools.NewState(shader.Starters()[0], testRenderer{})
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(s)
	t.Cleanup(m.close)
	return m
}
func TestSlidersAndInputFocus(t *testing.T) {
	m := testModel(t)
	m.key(tea.KeyPressMsg{Code: tea.KeyRight})
	if math.Abs(float64(m.state.Snapshot().Values[0]-.65)) > 1e-5 {
		t.Fatal("right did not nudge")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	if math.Abs(float64(m.state.Snapshot().Values[0]-1.15)) > 1e-5 {
		t.Fatal("shift did not nudge ×10")
	}
	for i := 0; i < 100; i++ {
		m.key(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
	}
	if m.state.Snapshot().Values[0] != 0 {
		t.Fatal("slider not clamped")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.selected != 1 {
		t.Fatal("selection did not move")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if m.playing {
		t.Fatal("space did not pause")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyTab})
	m.key(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m.key(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if m.input.Value() != "q " || m.playing {
		t.Fatal("prompt intercepted shortcut")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.input.Value() != "q " {
		t.Fatal("unavailable model consumed prompt")
	}
}
func TestClockAndRenderCoalescing(t *testing.T) {
	m := testModel(t)
	m.width = 100
	m.height = 32
	start := time.Now()
	m.lastTick = start
	m.Update(tickMsg(start.Add(100 * time.Millisecond)))
	if !m.rendering || math.Abs(float64(m.t-.1)) > 1e-5 {
		t.Fatal("clock/render not started")
	}
	m.Update(tickMsg(start.Add(200 * time.Millisecond)))
	if !m.dirty || m.renderCmd() != nil {
		t.Fatal("queued a second render")
	}
	m.playing = false
	m.Update(tickMsg(start.Add(time.Second)))
	if math.Abs(float64(m.t-.2)) > 1e-5 {
		t.Fatal("paused time advanced")
	}
	m.Update(frameMsg{img: image.NewNRGBA(image.Rect(0, 0, 8, 6)), revision: 0})
	if m.rendering {
		t.Fatal("glyph frame did not release slot")
	}
	cmd := m.renderCmd()
	if cmd == nil {
		t.Fatal("lost coalesced render")
	}
	frame := cmd().(frameMsg)
	if frame.img.Bounds().Dx() > 1536 || frame.img.Bounds().Dy() > 1024 {
		t.Fatal("oversized frame")
	}
	// A control edit after dispatch must suppress the stale result.
	m.state.Set("speed", 1)
	m.Update(frame)
	if m.rendering || !m.dirty {
		t.Fatal("stale frame wasn't dropped")
	}
}
func TestCommandsAndCancel(t *testing.T) {
	m := testModel(t)
	m.slash("/set speed 999")
	if m.state.Snapshot().Values[0] != 3 {
		t.Fatal("slash clamp")
	}
	m.slash("/pause")
	if m.playing {
		t.Fatal("pause")
	}
	m.slash("/play")
	if !m.playing {
		t.Fatal("play")
	}
	m.slash("/set speed NaN")
	if !strings.Contains(m.status, "finite") {
		t.Fatal("accepted NaN")
	}
	m.slash("/model")
	if !strings.Contains(m.status, "--no-engine") {
		t.Fatal("no-engine model message")
	}
	cmd := m.slash("/preset tunnel")
	if cmd == nil {
		t.Fatal("preset no-op")
	}
	msg := cmd()
	m.Update(msg)
	if m.state.Snapshot().Source.Name != "tunnel" {
		t.Fatal("preset not loaded")
	}
	m.gen, _ = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		<-ctx.Done()
		ch <- doneMsg{err: ctx.Err()}
	})
	m.key(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.gen.Canceled() {
		t.Fatal("escape did not cancel")
	}
}
func TestViewFitsAndNoControls(t *testing.T) {
	m := testModel(t)
	snap := m.state.Snapshot()
	src := snap.Source
	src.Params = nil
	src.ShadeBody = "return vec3<f32>(0.5);"
	if err := m.state.Replace(src, nil, snap.Revision); err != nil {
		t.Fatal(err)
	}
	m.selected = 12
	m.width = 80
	m.height = 24
	m.key(tea.KeyPressMsg{Code: tea.KeyDown})
	m.key(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.selected != 0 || !strings.Contains(m.View().Content, "PARAMETERS") {
		t.Fatal("empty panel")
	}
	m.width = 20
	if !strings.Contains(m.View().Content, "enlarge") {
		t.Fatal("small viewport")
	}
}

func TestPrepareHistoryReplacesBudgetNotices(t *testing.T) {
	history := []ds4.ChatMessage{
		{Role: "user", Content: "make a kaleidoscope"},
		{Role: "user", ToolCallID: "pad.tool-budget", Content: "No more tools"},
		{Role: "user", ToolCallID: bubble.ContextFeedbackID, Content: "old capacity"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "inspect the preview"},
	}
	got := prepareHistory(history, 0, 5)
	if len(got) != 4 || got[2].Content != "inspect the preview" || !strings.Contains(got[3].Content, "4 of 4") {
		t.Fatalf("stale budgets or lost request: %+v", got)
	}
	if history[1].Content != "No more tools" {
		t.Fatal("mutated caller history")
	}
}
