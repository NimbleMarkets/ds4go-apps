package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
)

// Explicit opt-in: loads a real model and exercises image prefill, generation,
// SVG edit tools, and the rerender loop without requiring an interactive TTY.
func TestVisualReviewLive(t *testing.T) {
	modelPath := os.Getenv("SVG_VISION_MODEL")
	if modelPath == "" {
		t.Skip("set SVG_VISION_MODEL to run a real vision model")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	lib, err := ds4.Load(os.Getenv("SVG_VISION_LIB"))
	if err != nil {
		t.Fatal(err)
	}
	opts := ds4.EngineOptions{ModelPath: modelPath, VisionPath: os.Getenv("SVG_VISION_ENCODER"), Backend: ds4.DetectDefaultBackend(""), WarmWeights: true}
	ds4.ApplyVisionDefaults(&opts)
	eng, err := lib.NewEngine(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if !eng.HasVision() {
		t.Fatal("model has no loaded vision encoder")
	}
	sess, err := eng.NewSession(16384)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	images := ds4.NewImageEncoder(eng)
	defer images.SetLimits(0, 0)
	m := newModel(&appinit.App{Lib: lib, EngineOpts: opts, Flags: &appinit.Flags{Ctx: 16384}, Logger: log.New(os.Stderr, "", 0), LogBuf: ds4log.NewBuffer(100)})
	m.engine = eng
	m.maxToolRounds = 4
	m.visual = visualOptions{Mode: "on", MaxPasses: 2}
	defer m.metadataCancel()
	draft := filepath.Join(dir, "draft.svg")
	if err := os.WriteFile(draft, []byte(visualFixture), 0644); err != nil {
		t.Fatal(err)
	}
	driver := bubble.NewGenerationDriver(bubble.DriverOptions{
		Engine: eng, Session: sess, Images: images, Tools: m.tools, MaxRounds: m.maxToolRounds + 1, MaxTokens: 1536,
		ExecuteTools: func(ctx context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
			return executeSVGTools(ctx, m.tools, calls, true)
		},
		PrepareHistory: prepareSVGHistory,
		OnEvent: func(e bubble.Event) {
			switch ev := e.(type) {
			case bubble.AssistantMessageEvent:
				t.Logf("assistant: %s calls=%+v", ev.Message.Content, ev.Message.ToolCalls)
			case bubble.ToolResultsEvent:
				t.Logf("tools: %+v", ev.Results)
			}
		},
	})
	first := true
	run := func(ctx context.Context, history []ds4.ChatMessage) (bubble.RunResult, error) {
		if first {
			first = false
			a := ds4.ChatMessage{Role: "assistant", Content: "Draft ready for review."}
			return bubble.RunResult{Assistant: a, History: append(history, a)}, nil
		}
		return driver.RunWithPrompt(ctx, m.systemPrompt(), history)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	_, report, err := runWithVisualReview(ctx, draft, []ds4.ChatMessage{{Role: "user", Content: "Draw a red square and the word Label, with both fully inside a 200 by 100 canvas and with comfortable margins. The existing draft needs visual inspection."}}, 2, 0, run, func(n int) { t.Logf("review pass %d", n) })
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s; final SVG: %s", report.Status, data)
	if report.Passes == 0 || validateSVG(data) != "valid" {
		t.Fatalf("invalid review: %+v", report)
	}
	if strings.Contains(string(data), `x="180"`) {
		t.Fatal("model left the deliberately clipped square unchanged")
	}
}
