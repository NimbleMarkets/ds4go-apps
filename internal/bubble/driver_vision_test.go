package bubble

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestDriverMultimodalGeneration(t *testing.T) {
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(ds4.EngineOptions{VisionPath: "encoder.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(2048)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	images := ds4.NewImageEncoder(eng)
	defer images.SetLimits(0, 0)
	history := []ds4.ChatMessage{{Role: "user", Parts: []ds4.ContentPart{{Text: "inspect"}, {Image: &ds4.ImageInput{Data: []byte("mock-image")}}}}}
	opts := DriverOptions{Engine: eng, Session: sess, Images: images, Tools: ds4.NewToolRegistry(), MaxTokens: 4}
	res, err := NewGenerationDriver(opts).RunWithPrompt(context.Background(), "", history)
	if err != nil {
		t.Fatal(err)
	}
	if ctl.MultimodalSyncCalls() == 0 || !sess.HasVisionState() || res.Assistant.Role != "assistant" {
		t.Fatal("image never reached the session")
	}
	// A token-only override must fail explicitly, never silently drop images.
	opts.CompleteTurn = func(*ds4.Tokens, ds4.GenerateOptions, func(dsml.StreamEvent)) (string, error) {
		t.Fatal("text override consumed an image prompt")
		return "", nil
	}
	_, err = NewGenerationDriver(opts).RunWithPrompt(context.Background(), "", history)
	if !errors.Is(err, ds4.ErrCompleteFuncCannotCarryImages) {
		t.Fatalf("error = %v", err)
	}
}

func TestDriverCarriesToolImagesIntoNextRound(t *testing.T) {
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(ds4.EngineOptions{VisionPath: "encoder.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(2048)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	images := ds4.NewImageEncoder(eng)
	defer images.SetLimits(0, 0)
	reg := ds4.NewToolRegistry()
	if err := reg.Register(ds4.MultimodalTool{
		ToolSchema: ds4.ToolSchema{Name: "preview", Description: "render SVG"},
		Handler:    func(context.Context, json.RawMessage) (ds4.ToolResult, error) { return ds4.ToolResult{}, nil },
	}); err != nil {
		t.Fatal(err)
	}
	block, err := dsml.RenderToolCalls([]dsml.ToolCall{{Name: "preview", Arguments: `{}`}})
	if err != nil {
		t.Fatal(err)
	}
	turn := 0
	opts := DriverOptions{Engine: eng, Session: sess, Images: images, Tools: reg,
		ExecuteTools: func(_ context.Context, calls []ds4.ToolCall) ([]ds4.ChatMessage, error) {
			return []ds4.ChatMessage{{Role: "tool", ToolCallID: calls[0].ID, Parts: []ds4.ContentPart{{Text: "rendered"}, {Image: &ds4.ImageInput{Data: []byte("preview")}}}}}, nil
		},
		CompletePrompt: func(p *ds4.Prompt, _ ds4.GenerateOptions, _ func(dsml.StreamEvent)) (string, error) {
			turn++
			if turn == 1 {
				if len(p.Images) != 0 {
					t.Fatal("unexpected initial image")
				}
				return block, nil
			}
			if len(p.Images) != 1 {
				t.Fatalf("image spans = %d", len(p.Images))
			}
			return "inspected", nil
		},
	}
	res, err := NewGenerationDriver(opts).RunWithPrompt(context.Background(), "", []ds4.ChatMessage{{Role: "user", Content: "draw"}})
	if err != nil || turn != 2 || res.Assistant.Content != "inspected" {
		t.Fatalf("turns=%d res=%+v err=%v", turn, res, err)
	}
}
