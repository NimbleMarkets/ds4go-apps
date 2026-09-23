package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/memory"
)

func TestContextDisplayDistinguishesConfiguredAndMeasured(t *testing.T) {
	m := testModel(t)
	m.app = &appinit.App{Flags: &appinit.Flags{Ctx: 262144}}
	m.noEngine = false
	if got := m.contextLabel(); got != "context 262144 tokens configured" {
		t.Fatal(got)
	}
	m.contextUsage = bubble.ContextUsageEvent{PromptTokens: 9476, Capacity: 16384}
	if got := m.contextLabel(); got != "prompt 9476/16384 tokens" {
		t.Fatal("must display measured allocation, not new configuration:", got)
	}
	for _, want := range []string{"6908 remain", "not the model's maximum", "--ctx", "131072", "system instructions"} {
		if !strings.Contains(m.contextDetails(), want) {
			t.Fatal("missing context explanation:", want)
		}
	}
}

func TestMemoryDialogAndManualCompaction(t *testing.T) {
	m := testModel(t)
	mem, err := memory.Open(t.TempDir(), "ui-session")
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	m.memory = mem
	m.width, m.height = 100, 35
	m.input.Focus()
	m.input.SetValue("pending prompt")
	m.input.SetCursor(3)
	cmd := m.slash("/memory")
	if cmd == nil {
		t.Fatal("no memory command")
	}
	m.Update(cmd())
	if m.inspect.kind != "memory" || !strings.Contains(m.View().Content, "ui-session") {
		t.Fatal("memory dialog missing session")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.input.Value() != "pending prompt" || m.input.Position() != 3 || !m.input.Focused() {
		t.Fatal("dialog lost prompt/focus")
	}
	m.history = []ds4.ChatMessage{{Role: "user", Content: "keep the request"}}
	cmd = m.slash("/compact")
	if cmd == nil {
		t.Fatal("no compact command")
	}
	m.Update(cmd())
	if m.compactions != 1 || m.history[len(m.history)-1].Content != "keep the request" {
		t.Fatal("manual compaction lost request")
	}
	m.gen, _ = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) { defer close(ch); <-ctx.Done() })
	if m.slash("/compact") != nil {
		t.Fatal("allowed concurrent history mutation")
	}
	m.gen.StopAndWait()
	m.gen = nil
}

func TestLeanHistoryRetainsIDsAndLatestImage(t *testing.T) {
	im := &ds4.ImageInput{Data: []byte("image")}
	h := []ds4.ChatMessage{
		{Role: "assistant", ReasoningContent: "reasoning", ToolCalls: []ds4.ToolCall{{ID: "a", Name: "trip_preview"}}},
		{Role: "tool", ToolCallID: "a", Parts: []ds4.ContentPart{{Image: im}, {Text: "first preview"}}},
		{Role: "tool", ToolCallID: "b", Content: "ERROR: " + strings.Repeat("error ", 3000)},
		{Role: "tool", ToolCallID: "c", Parts: []ds4.ContentPart{{Image: im}}},
	}
	got := leanHistory(h)
	if got[0].ReasoningContent != "" || got[1].ToolCallID != "a" || got[1].Parts[0].Image != nil || got[3].Parts[0].Image != im || len(got[2].Content) > 4100 {
		t.Fatalf("bad pruning: %+v", got)
	}
	if h[0].ReasoningContent == "" || h[1].Parts[0].Image != im || len(h[2].Content) < 10000 {
		t.Fatal("mutated original history")
	}
}

func TestCompactionPersistsCheckpointAndPreservesRequestAndToolGroup(t *testing.T) {
	m := testModel(t)
	mem, err := memory.Open(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	if err := mem.Set(context.Background(), "plan", "keep symmetry; next adjust speed"); err != nil {
		t.Fatal(err)
	}
	request := strings.Repeat("保留蓝色", 1000)
	h := []ds4.ChatMessage{
		{Role: "user", Content: "no strobing"},
		{Role: "assistant", ToolCalls: []ds4.ToolCall{{ID: "old", Name: "trip_set_shader", Arguments: strings.Repeat("old source ", 5000)}}},
		{Role: "tool", ToolCallID: "old", Content: "compile OK: blue"},
		{Role: "user", Content: request},
		{Role: "assistant", ToolCalls: []ds4.ToolCall{{ID: "recent", Name: "trip_set_param", Arguments: `{"name":"speed","value":1}`}}},
		{Role: "tool", ToolCallID: "recent", Content: "speed = 1"},
	}
	got, err := compactContext(context.Background(), h, m.state, mem)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || !reflect.DeepEqual(got[2:], h[3:]) {
		t.Fatalf("lost current request/group: %d", len(got))
	}
	if !strings.Contains(got[0].Content, "no strobing") || !strings.Contains(got[0].Content, "compile OK") || !strings.Contains(got[1].Content, "keep symmetry") {
		t.Fatal("lost checkpoint/plan")
	}
	out, err := mem.Call(context.Background(), "session", "get", []byte(`{"key":"checkpoint"}`))
	if err != nil || !strings.Contains(out, "no strobing") {
		t.Fatalf("checkpoint not durable: %s %v", out, err)
	}
	if len(h[1].ToolCalls[0].Arguments) < 10000 {
		t.Fatal("modified original")
	}
}
