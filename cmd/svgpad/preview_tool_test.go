package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/appinit"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/ds4log"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestSVGPreviewResultAndFailures(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"valid", visualFixture, "Current draft.svg"},
		{"missing", "", "draft is empty"},
		{"empty", "  \n", "draft is empty"},
		{"incomplete", svgOpen, "Invalid SVG"},
		{"post-root", svgOpen + "</svg><rect/>", "root closed"},
		{"render error", badColorSVG, "Preview rendering failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "draft.svg")
			if tc.name != "missing" {
				if err := os.WriteFile(path, []byte(tc.doc), 0644); err != nil {
					t.Fatal(err)
				}
			}
			reg := ds4.NewToolRegistry()
			reg.MustRegister(svgPreviewTool(path))
			results, err := executeSVGTools(context.Background(), reg, []ds4.ToolCall{{ID: "preview-1", Name: svgPreviewName, Arguments: `{}`}}, true)
			if err != nil || len(results) != 1 {
				t.Fatalf("results=%+v err=%v", results, err)
			}
			msg := results[0]
			if msg.Role != "tool" || msg.ToolCallID != "preview-1" || !strings.Contains(svgToolResultText(msg), tc.want) {
				t.Fatalf("unexpected result: %+v", msg)
			}
			if tc.name == "valid" {
				if len(msg.Parts) != 2 || msg.Parts[1].Image == nil {
					t.Fatalf("missing image: %+v", msg)
				}
				img, err := png.Decode(bytes.NewReader(msg.Parts[1].Image.Data))
				if err != nil {
					t.Fatal(err)
				}
				if img.Bounds().Dx() != 1024 || img.Bounds().Dy() != 512 {
					t.Fatalf("bounds: %v", img.Bounds())
				}
				if !strings.Contains(svgToolResultText(msg), "[preview image]") {
					t.Fatal("UI summary omitted image")
				}
			} else if len(msg.Parts) != 0 {
				t.Fatal("failure must return text diagnostics")
			}
			data, err := os.ReadFile(path)
			if tc.name == "missing" {
				if !os.IsNotExist(err) {
					t.Fatal("preview created a draft")
				}
			} else if err != nil || string(data) != tc.doc {
				t.Fatal("preview changed the draft")
			}
		})
	}
}

func TestSVGPreviewUnavailableKeepsOtherToolsAndCancellation(t *testing.T) {
	reg := ds4.NewToolRegistry()
	reg.MustRegister(svgPreviewTool("missing.svg"))
	called := false
	reg.MustRegister(ds4.Tool{ToolSchema: ds4.ToolSchema{Name: "edit"}, Handler: func(context.Context, json.RawMessage) (string, error) { called = true; return "edited", nil }})
	calls := []ds4.ToolCall{{ID: "p", Name: svgPreviewName, Arguments: `{}`}, {ID: "e", Name: "edit", Arguments: `{}`}}
	results, err := executeSVGTools(context.Background(), reg, calls, false)
	if err != nil || len(results) != 2 || !called {
		t.Fatalf("mixed batch: %+v, %v", results, err)
	}
	if results[0].ToolCallID != "p" || len(results[0].Parts) != 0 || !strings.Contains(results[0].Content, "vision model") || results[1].Content != "edited" {
		t.Fatalf("results: %+v", results)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called = false
	if _, err := executeSVGTools(ctx, reg, calls, true); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancellation: %v, called=%v", err, called)
	}
}

func TestSVGPreviewReachesModelAfterEdits(t *testing.T) {
	t.Chdir(t.TempDir())
	m := newModel(&appinit.App{Flags: &appinit.Flags{Ctx: 8192}, Logger: log.New(io.Discard, "", 0), LogBuf: ds4log.NewBuffer(100)})
	defer m.metadataCancel()
	path := filepath.Join(m.workDir, "draft.svg")
	if err := os.WriteFile(path, []byte(visualFixture), 0644); err != nil {
		t.Fatal(err)
	}
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(ds4.EngineOptions{VisionPath: "encoder.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	m.engine = eng
	m.maxToolRounds = 3
	m.visual = visualOptions{Mode: "on", MaxPasses: 3}
	sess, err := eng.NewSession(8192)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	images := ds4.NewImageEncoder(eng)
	defer images.SetLimits(0, 0)
	toolBlock := func(calls ...dsml.ToolCall) string {
		t.Helper()
		block, err := dsml.RenderToolCalls(calls)
		if err != nil {
			t.Fatal(err)
		}
		return block
	}
	first := toolBlock(dsml.ToolCall{Name: svgPreviewName, Arguments: `{}`})
	edit := toolBlock(dsml.ToolCall{Name: "svg_replace", Arguments: `{"target":"x=\"180\"","replacement":"x=\"150\""}`}, dsml.ToolCall{Name: svgPreviewName, Arguments: `{}`})
	turn := 0
	var pngs [][]byte
	driver := bubble.NewGenerationDriver(bubble.DriverOptions{
		Engine: eng, Session: sess, Images: images, Tools: m.tools, MaxRounds: m.maxToolRounds + 1,
		PrepareHistory: prepareSVGHistory,
		ExecuteTools: func(ctx context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
			results, err := executeSVGTools(ctx, m.tools, calls, true)
			for _, msg := range results {
				for _, part := range msg.Parts {
					if part.Image != nil {
						pngs = append(pngs, part.Image.Data)
					}
				}
			}
			return results, err
		},
		CompletePrompt: func(p *ds4.Prompt, _ ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
			turn++
			if turn == 1 {
				if len(p.Images) != 0 {
					t.Fatal("unexpected starting image")
				}
				return first, nil
			}
			if len(p.Images) != 1 {
				t.Fatalf("round %d has %d images, want latest preview only", turn, len(p.Images))
			}
			if turn == 2 {
				return edit, nil
			}
			return "Inspected the revised drawing.", nil
		},
	})
	res, err := driver.RunWithPrompt(context.Background(), m.systemPrompt(), []ds4.ChatMessage{{Role: "user", Content: "Fix the clipped square."}})
	if err != nil || turn != 3 || res.ToolRounds != 2 {
		t.Fatalf("turn=%d rounds=%d err=%v", turn, res.ToolRounds, err)
	}
	if len(pngs) != 2 || bytes.Equal(pngs[0], pngs[1]) {
		t.Fatal("preview did not reflect the edited SVG")
	}
	count := 0
	for _, msg := range res.History {
		for _, p := range msg.Parts {
			if p.Image != nil {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("retained %d preview images", count)
	}
}

func TestPreviewHistoryPruningPreservesOtherImagesAndText(t *testing.T) {
	parts := func(text string) []ds4.ContentPart {
		return []ds4.ContentPart{{Text: text}, {Image: &ds4.ImageInput{Data: []byte(text)}}}
	}
	history := []ds4.ChatMessage{
		{Role: "user", Parts: parts("user reference")},
		{Role: "user", ToolCallID: visualFeedbackID, Parts: parts("automatic review")},
		{Role: "assistant", ToolCalls: []ds4.ToolCall{{ID: "p1", Name: svgPreviewName}, {ID: "other", Name: "other_tool"}}},
		{Role: "tool", ToolCallID: "p1", Parts: parts("old preview")},
		{Role: "tool", ToolCallID: "other", Parts: parts("other tool")},
		{Role: "assistant", ToolCalls: []ds4.ToolCall{{ID: "p2", Name: svgPreviewName}}},
		{Role: "tool", ToolCallID: "p2", Parts: parts("latest preview")},
	}
	got := latestVisualImage(history)
	for _, i := range []int{0, 4, 6} {
		if len(got[i].Parts) == 0 {
			t.Errorf("image %d lost", i)
		}
	}
	for _, i := range []int{1, 3} {
		if len(got[i].Parts) != 0 {
			t.Errorf("stale image %d retained", i)
		}
	}
	if !strings.Contains(got[3].Content, "old preview") {
		t.Fatal("tool text lost")
	}
	cleared := withoutVisualImages(history)
	if len(cleared[6].Parts) != 0 || len(cleared[0].Parts) == 0 || len(cleared[4].Parts) == 0 {
		t.Fatal("cross-request cleanup removed unrelated images or kept preview")
	}
	for _, i := range []int{1, 3, 6} {
		if len(history[i].Parts) != 2 {
			t.Fatal("original history mutated")
		}
	}
	// A later automatic pass supersedes even the latest tool preview.
	got = latestVisualImage(append(history, ds4.ChatMessage{Role: "user", ToolCallID: visualFeedbackID, Parts: parts("new review")}))
	if len(got[6].Parts) != 0 || len(got[7].Parts) == 0 {
		t.Fatal("automatic review did not supersede tool preview")
	}
}
