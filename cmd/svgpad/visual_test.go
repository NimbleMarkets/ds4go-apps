package main

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
)

const visualFixture = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100"><rect x="180" y="20" width="40" height="40" fill="red"/><text x="10" y="60" font-size="24">Label</text></svg>`

func TestVisionEncoderAliasAndPath(t *testing.T) {
	t.Setenv("DS4_DIR", t.TempDir())
	if err := os.MkdirAll(ds4.DefaultModelsDir(), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ds4.DefaultModelsDir(), "GLM-5.3-Flash-Vision-Encoder.gguf")
	if err := os.WriteFile(path, []byte("encoder fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"glm53-vision", path} {
		opts := visualOptions{Mode: "on", MaxPasses: 3, EncoderPath: value}
		if err := opts.resolveAndValidate(); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if opts.EncoderPath != path {
			t.Fatalf("%q resolved to %q, want %q", value, opts.EncoderPath, path)
		}
	}
	// Omitting --vision leaves companion selection to ApplyVisionDefaults.
	opts := visualOptions{Mode: "on", MaxPasses: 3}
	if err := opts.resolveAndValidate(); err != nil || opts.EncoderPath != "" {
		t.Fatalf("automatic companion selection changed: %+v, %v", opts, err)
	}
	for _, value := range []string{"unknown-encoder", "v41-vision", filepath.Join(t.TempDir(), "missing.gguf"), t.TempDir()} {
		opts.EncoderPath = value
		if err := opts.resolveAndValidate(); err == nil {
			t.Errorf("accepted missing/invalid encoder %q", value)
		}
	}
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	opts.EncoderPath = "glm53-vision"
	if err := opts.resolveAndValidate(); err == nil {
		t.Fatal("accepted an empty encoder file")
	}
}

func TestVisualPreviewPreservesCanvasAndRendersLabels(t *testing.T) {
	input, err := renderVisualPreview([]byte(visualFixture))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(input.Data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != 1024 || b.Dy() != 512 {
		t.Fatalf("bounds = %v", b)
	}
	r, g, bl, a := img.At(0, 0).RGBA()
	if r != 65535 || g != r || bl != r || a != r {
		t.Fatal("transparent canvas was not composited onto white")
	}
	ink := 0
	for y := 100; y < 350; y++ {
		for x := 20; x < 500; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r < 32000 && g < 32000 && b < 32000 {
				ink++
			}
		}
	}
	if ink < 100 {
		t.Fatalf("label missing from review image: %d dark pixels", ink)
	}
}

func TestVisualReviewRevisesAndRechecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.svg")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(visualFixture)
	turn := 0
	var previews [][]byte
	run := func(_ context.Context, history []ds4.ChatMessage) (bubble.RunResult, error) {
		turn++
		if turn > 1 {
			images := 0
			for _, m := range history {
				for _, p := range m.Parts {
					if p.Image != nil {
						images++
						previews = append(previews, p.Image.Data)
					}
				}
			}
			if images != 1 {
				t.Fatalf("turn %d has %d images, want only latest", turn, images)
			}
			if history[len(history)-1].ToolCallID != visualFeedbackID {
				t.Fatal("review feedback confused with user request")
			}
		}
		if turn == 2 {
			write(strings.Replace(visualFixture, `x="180"`, `x="140"`, 1))
		}
		answer := ds4.ChatMessage{Role: "assistant", Content: "reviewed"}
		return bubble.RunResult{Assistant: answer, History: append(history, answer), ToolRounds: 1}, nil
	}
	res, report, err := runWithVisualReview(context.Background(), path, []ds4.ChatMessage{{Role: "user", Content: "draw a label and box"}}, 3, 0, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn != 3 || report.Passes != 2 || !strings.Contains(report.Status, "complete") || res.ToolRounds != 3 {
		t.Fatalf("turns=%d report=%+v rounds=%d", turn, report, res.ToolRounds)
	}
	if len(previews) != 2 || bytes.Equal(previews[0], previews[1]) {
		t.Fatal("revision was not rerendered")
	}
}

// The critique must ask picture questions, not only chart questions —
// experiment H (2026-09-15) showed pictorial flaws (floating grass, wrong
// proportions) are the dominant failure mode the review needs to catch.
func TestVisualReviewCritiqueAsksPictorialQuestions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.svg")
	if err := os.WriteFile(path, []byte(visualFixture), 0644); err != nil {
		t.Fatal(err)
	}
	var feedback string
	run := func(_ context.Context, history []ds4.ChatMessage) (bubble.RunResult, error) {
		if last := history[len(history)-1]; last.ToolCallID == visualFeedbackID {
			feedback = last.Content
		}
		answer := ds4.ChatMessage{Role: "assistant", Content: "reviewed"}
		return bubble.RunResult{Assistant: answer, History: append(history, answer)}, nil
	}
	history := []ds4.ChatMessage{{Role: "user", Content: "draw a llama"}}
	if _, _, err := runWithVisualReview(context.Background(), path, history, 2, 0, run, nil); err != nil {
		t.Fatal(err)
	}
	if feedback == "" {
		t.Fatal("no review feedback captured")
	}
	low := strings.ToLower(feedback)
	for _, want := range []string{"proportion", "grounded", "palette", "recognizable"} {
		if !strings.Contains(low, want) {
			t.Errorf("critique lacks pictorial criterion %q", want)
		}
	}
}

// A text-only engine must warn loudly instead of silently skipping review.
func TestVisionWarning(t *testing.T) {
	cases := []struct {
		mode      string
		hasVision bool
		want      bool
	}{
		{"auto", false, true},
		{"auto", true, false},
		{"off", false, false},
		{"on", false, true},
		{"on", true, false},
	}
	for _, tc := range cases {
		got := visionWarning(tc.mode, tc.hasVision)
		if (got != "") != tc.want {
			t.Errorf("visionWarning(%q, %v) = %q, want warning=%v", tc.mode, tc.hasVision, got, tc.want)
		}
	}
}

func TestVisualReviewBoundsFailuresAndFallback(t *testing.T) {
	for _, scenario := range []string{"limit", "invalid", "cancel", "failure", "response-svg", "already-used"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "draft.svg")
			if scenario != "response-svg" {
				if err := os.WriteFile(path, []byte(visualFixture), 0644); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			turn, used := 0, 0
			if scenario == "already-used" {
				used = 2
			}
			failure := errors.New("generation failed")
			run := func(_ context.Context, history []ds4.ChatMessage) (bubble.RunResult, error) {
				turn++
				if scenario == "cancel" {
					cancel()
				}
				if scenario == "invalid" {
					if err := os.WriteFile(path, []byte("<svg>"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "failure" && turn == 2 {
					return bubble.RunResult{}, failure
				}
				if scenario == "limit" && turn > 1 {
					data, _ := os.ReadFile(path)
					if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
						t.Fatal(err)
					}
				}
				a := ds4.ChatMessage{Role: "assistant", Content: visualFixture}
				return bubble.RunResult{Assistant: a, History: append(history, a)}, nil
			}
			res, report, err := runWithVisualReview(ctx, path, nil, 2, used, run, nil)
			switch scenario {
			case "cancel":
				if !errors.Is(err, context.Canceled) || turn != 1 {
					t.Fatalf("err=%v turns=%d", err, turn)
				}
			case "failure":
				if !errors.Is(err, failure) || len(res.History) == 0 {
					t.Fatalf("lost partial history on failure: %+v %v", res, err)
				}
			case "invalid":
				if err != nil || report.Passes != 0 || turn != 1 {
					t.Fatalf("reviewed invalid SVG: %+v %v", report, err)
				}
			case "limit", "already-used":
				if err != nil || report.Passes != 2 || !strings.Contains(report.Status, "limit reached after edits") || turn != 3-used {
					t.Fatalf("budget: %+v turns=%d %v", report, turn, err)
				}
			case "response-svg":
				if err != nil || report.Passes != 1 {
					t.Fatalf("fallback: %+v %v", report, err)
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestWithoutVisualImagesDoesNotMutateHistory(t *testing.T) {
	h := []ds4.ChatMessage{{Role: "user", Content: "original"}, {Role: "user", ToolCallID: visualFeedbackID, Parts: []ds4.ContentPart{{Image: &ds4.ImageInput{Data: []byte("png")}}}}}
	clean := withoutVisualImages(h)
	if clean[1].Parts != nil || h[1].Parts[0].Image == nil || clean[0].Content != "original" {
		t.Fatal("history mutated or image retained")
	}
}
