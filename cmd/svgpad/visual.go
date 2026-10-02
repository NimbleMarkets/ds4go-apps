package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
)

const visualPreviewEdge = 1024
const maxVisualPasses = 5

// A marker distinguishes application feedback from the user's actual request
// when choosing titles, saved entry prompts, and metadata.
const visualFeedbackID = "svgpad.visual-review"

type visualOptions struct {
	Mode        string
	MaxPasses   int
	EncoderPath string
}

func (o *visualOptions) resolveAndValidate() error {
	switch o.Mode {
	case "auto", "on", "off":
	default:
		return fmt.Errorf("--visual-review must be auto, on, or off")
	}
	if o.MaxPasses < 1 || o.MaxPasses > maxVisualPasses {
		return fmt.Errorf("--visual-rounds must be between 1 and %d", maxVisualPasses)
	}
	if o.EncoderPath != "" {
		path := ds4.ResolveModelPath(o.EncoderPath)
		if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			return fmt.Errorf("vision encoder not found or empty: %s", o.EncoderPath)
		}
		o.EncoderPath = path
	}
	return nil
}

type visualReport struct {
	Passes int
	Status string
}

// visionWarning is the loud engine-open notice for a review mode that cannot
// run: without it, --visual-review auto silently no-ops on a text-only model
// and the agent's svg_preview calls fail every turn (experiment E, 2026-09-15).
func visionWarning(mode string, hasVision bool) string {
	if mode == "off" || hasVision {
		return ""
	}
	return "vision unavailable: visual review and svg_preview are disabled. Use a vision model (e.g. --model vision-q2) or pass --visual-review off."
}

type visualReviewMsg struct{ pass, limit int }

// renderVisualPreview uses the viewer's renderer and preserves the canvas
// aspect ratio. Composite transparency onto a known background so encoders
// that discard alpha do not turn transparent pixels into black ink.
func renderVisualPreview(data []byte) (ds4.ImageInput, error) {
	img, err := rasterizeChecked(data, visualPreviewEdge, visualPreviewEdge)
	if err != nil {
		return ds4.ImageInput{}, fmt.Errorf("render visual preview: %w", err)
	}
	opaque := image.NewRGBA(img.Bounds())
	draw.Draw(opaque, opaque.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(opaque, opaque.Bounds(), img, img.Bounds().Min, draw.Over)
	return ds4.ImageInputPNG(opaque)
}

// Remove previous requests' images while keeping critiques and edits as text.
func withoutVisualImages(history []ds4.ChatMessage) []ds4.ChatMessage {
	return pruneVisualImages(history, false)
}

// Keep only the newest automatic-review or svg_preview image per model round.
// Unrelated user images and other tools' images are preserved.
func latestVisualImage(history []ds4.ChatMessage) []ds4.ChatMessage {
	return pruneVisualImages(history, true)
}

func pruneVisualImages(history []ds4.ChatMessage, keepLatest bool) []ds4.ChatMessage {
	previewCalls := make(map[string]bool)
	for _, msg := range history {
		for _, call := range msg.ToolCalls {
			if call.Name == svgPreviewName {
				previewCalls[call.ID] = true
			}
		}
	}
	// Copy the slice: the UI may still be holding the input history.
	out := append([]ds4.ChatMessage(nil), history...)
	for i := len(out) - 1; i >= 0; i-- {
		automatic := out[i].ToolCallID == visualFeedbackID
		if !automatic && !(out[i].Role == "tool" && previewCalls[out[i].ToolCallID]) {
			continue
		}
		hasImage := false
		for _, part := range out[i].Parts {
			hasImage = hasImage || part.Image != nil
		}
		if !hasImage {
			continue
		}
		if keepLatest {
			keepLatest = false
			continue
		}
		if automatic {
			out[i].Parts = nil
			out[i].Content = "Earlier application-requested visual review (preview omitted)."
		} else {
			var text strings.Builder
			for _, part := range out[i].Parts {
				text.WriteString(part.Text)
			}
			out[i].Content = text.String() + "\n[Earlier preview image omitted.]"
			out[i].Parts = nil
		}
	}
	return out
}

type svgTurnRunner func(context.Context, []ds4.ChatMessage) (bubble.RunResult, error)

// runWithVisualReview always lets the normal drafting loop finish first.
// Then each valid revision is inspected, even when the model neglected to
// request validation. Unchanged SVG ends the loop; changed SVG is rendered
// again. Invalid revisions return to the existing syntax-correction gate.
// used carries the budget across those correction restarts.
func runWithVisualReview(ctx context.Context, draftPath string, history []ds4.ChatMessage, limit, used int, run svgTurnRunner, progress func(int)) (bubble.RunResult, visualReport, error) {
	report := visualReport{Passes: used}
	res, err := run(ctx, withoutVisualImages(history))
	if err != nil {
		return res, report, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return res, report, err
		}
		data, err := os.ReadFile(draftPath)
		if os.IsNotExist(err) || (err == nil && len(data) == 0) {
			// Support the existing fallback for models that output SVG in
			// their answer. Put it in the draft so edit tools can revise it.
			data = extractSVG(res.Assistant.Content)
			if len(data) > 0 {
				err = os.WriteFile(draftPath, data, 0644)
			} else {
				err = nil
			}
		}
		if err != nil {
			return res, report, fmt.Errorf("visual review draft: %w", err)
		}
		if validateSVG(data) != "valid" {
			report.Status = "visual review pending valid SVG"
			return res, report, nil
		}
		if report.Passes >= limit {
			report.Status = "automatic review limit reached after edits"
			return res, report, nil
		}
		img, err := renderVisualPreview(data)
		if err != nil {
			return res, report, err
		}
		report.Passes++
		feedback := fmt.Sprintf("Application visual review, pass %d of %d. The attached image is the current draft.svg rendered on white. Judge it as a picture against the user's request: Is every object recognizable at a glance? Are proportions right — parts sized and attached where they belong? Is everything grounded — resting on its surface, not floating or sunk? Is the depth order correct, with nothing overlapping what should be in front of it? Does the palette cohere with enough contrast to read? Also check for clipped content, stray elements, and unreadable text. Name the single most visible flaw first, then fix the concrete issues using svg_read and the replacement tools; validate any edits. If the image already serves the request well, say so. Do not clear or rebuild the draft. This is review of the existing drawing, not a new drawing request.", report.Passes, limit)
		if report.Passes == limit {
			feedback += " This is the final automatic image review pass; further edits will not receive another automatic check. Use svg_preview if needed and mention any remaining uncertainty."
		}
		nextHistory := append(withoutVisualImages(res.History), ds4.ChatMessage{
			Role: "user", ToolCallID: visualFeedbackID, Content: feedback,
			Parts: []ds4.ContentPart{{Text: feedback}, {Image: &img}},
		})
		if progress != nil {
			progress(report.Passes)
		}
		next, err := run(ctx, nextHistory)
		next.ToolRounds += res.ToolRounds
		if len(next.History) > 0 {
			res = next
		}
		if err != nil {
			report.Status = "visual review interrupted"
			return res, report, err
		}
		after, err := os.ReadFile(draftPath)
		if err != nil {
			return res, report, fmt.Errorf("read reviewed SVG: %w", err)
		}
		if bytes.Equal(data, after) {
			report.Status = fmt.Sprintf("visual review complete (%d/%d)", report.Passes, limit)
			return res, report, nil
		}
	}
}
