package main

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go/dsml"
	"github.com/charmbracelet/x/ansi"
)

func TestInspectorsPreservePromptAndCopySource(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 30
	m.input.Focus()
	m.input.SetValue("keep this prompt")
	m.input.SetCursor(4)
	m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if m.inspect.kind != "source" || !strings.Contains(m.View().Content, "Shade body") {
		t.Fatal("source shortcut failed from prompt")
	}
	_, original := m.sourceText()
	_, copyCmd := m.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if copyCmd == nil || !strings.Contains(m.inspect.notice, "clipboard") {
		t.Fatal("missing shared copy action")
	}
	// Switching tabs does not consume the prompt's Tab focus action.
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	_, wgsl := m.sourceText()
	if !strings.Contains(wgsl, "@compute") || !strings.Contains(wgsl, original) {
		t.Fatal("full WGSL doesn't include the active shader")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.state.Set("warp", 1.5)
	_, data := m.sourceText()
	var preset params.Preset
	if err := json.Unmarshal([]byte(data), &preset); err != nil || preset.Values["warp"] != 1.5 {
		t.Fatalf("source doesn't show current preset values: %s", data)
	}
	snap := m.state.Snapshot()
	next := shader.Starters()[1]
	if err := m.state.Replace(next, nil, snap.Revision); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.View().Content, next.Name) {
		t.Fatal("source didn't follow successful edit")
	}
	m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if m.inspect.kind != "thinking" {
		t.Fatal("can't switch directly from source to thinking")
	}
	m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.inspect.kind != "" || m.input.Value() != "keep this prompt" || m.input.Position() != 4 || !m.input.Focused() {
		t.Fatal("inspection changed prompt text/focus/cursor")
	}
}

func TestSourcePagerWidthHighlightAndCache(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 180, 30
	m.inspect = inspector{kind: "source"}
	snap := m.state.Snapshot()
	src := snap.Source
	src.ShadeBody = "/* " + strings.Repeat("colored comment ", 20) + " */\nreturn vec3<f32>(1.0);"
	if err := m.state.Replace(src, nil, snap.Revision); err != nil {
		t.Fatal(err)
	}
	lines := m.inspectorLines()
	if len(lines) != 3 || ansi.StringWidth(lines[0]) <= 88 || !strings.Contains(lines[1], "\x1b[") {
		t.Fatalf("source not using wide highlighted pager: %q", lines)
	}
	if again := m.inspectorLines(); &again[0] != &lines[0] {
		t.Fatal("unchanged source wasn't cached")
	}
	_, raw := m.sourceText()
	if raw != src.ShadeBody || strings.Contains(raw, "\x1b[") {
		t.Fatal("display changed raw/copy source")
	}
	m.width = 60
	if len(m.inspectorLines()) <= len(lines) {
		t.Fatal("resize failed to rewrap cached source")
	}
	m.input.Focus()
	m.input.SetValue("keep draft")
	m.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.inspect.kind != "" || m.input.Value() != "keep draft" || !m.input.Focused() {
		t.Fatal("pager shortcuts edited prompt or failed to close")
	}
}

func TestActivityStreamsWithoutDuplicationAndSurvivesCancellation(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 25
	m.Update(driverMsg{event: bubble.RoundStartedEvent{Round: 0}})
	for _, ev := range []dsml.StreamEvent{
		{Type: dsml.EventReasoningDelta, Delta: "plan "},
		{Type: dsml.EventReasoningDelta, Delta: "the colors"},
		{Type: dsml.EventContentDelta, Delta: "Here is the shader."},
		{Type: dsml.EventToolCallStart, Name: "trip_set_shader"},
	} {
		m.Update(driverMsg{event: bubble.StreamEvent{Event: ev}})
	}
	m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	view := m.View().Content
	for _, text := range []string{"plan the colors", "Here is the shader.", "trip_set_shader"} {
		if !strings.Contains(view, text) {
			t.Fatalf("missing live activity %q", text)
		}
	}
	m.Update(driverMsg{event: bubble.AssistantMessageEvent{Message: ds4.ChatMessage{ReasoningContent: "plan the colors", Content: "Here is the shader."}}})
	if strings.Count(m.activityText(), "plan the colors") != 1 {
		t.Fatal("final message duplicated streamed reasoning")
	}
	m.Update(doneMsg{})
	if !strings.Contains(m.activityText(), "plan the colors") {
		t.Fatal("completion discarded activity")
	}
	m.applyStream(dsml.StreamEvent{Type: dsml.EventContentDelta, Delta: strings.Repeat("more lines\n", 100)})
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	top := m.inspect.top
	m.applyStream(dsml.StreamEvent{Type: dsml.EventContentDelta, Delta: "new output\n"})
	if top < 0 || m.inspect.top != top {
		t.Fatal("streaming stole scroll position")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.inspect.top != -1 {
		t.Fatal("End doesn't resume following")
	}
}

func TestReasoningControlsAndBoundedSanitizedActivity(t *testing.T) {
	m := testModel(t)
	m.activeThinkMode = ds4.ThinkNone
	for _, want := range []ds4.ThinkMode{ds4.ThinkHigh, ds4.ThinkMax, ds4.ThinkNone} {
		m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
		if m.thinkMode != want || m.activeThinkMode != ds4.ThinkNone {
			t.Fatal("reasoning didn't cycle independently of active request")
		}
	}
	for i := 0; i < 30; i++ {
		m.startActivityRound(i)
	}
	m.applyStream(dsml.StreamEvent{Type: dsml.EventReasoningDelta, Delta: strings.Repeat("界", 40000)})
	if len(m.activity) != 20 || len([]rune(m.currentActivity().thinking)) > 32100 {
		t.Fatal("activity memory is unbounded")
	}
	if text := cleanInspectText("safe\x1b[2J\x00\r\ntext"); text != "safe\ntext" {
		t.Fatalf("terminal controls not removed: %q", text)
	}
}
