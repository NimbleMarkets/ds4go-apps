package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
)

const svgPreviewName = "svg_preview"

func svgPreviewTool(draftPath string) ds4.MultimodalTool {
	return ds4.MultimodalTool{
		ToolSchema: ds4.ToolSchema{
			Name:        svgPreviewName,
			Description: "Render the current complete draft SVG and return its image for visual inspection. Check clipping, overlapping labels, spacing, and composition, then use replacement tools to fix visible problems. Requires a vision model with its encoder and visual review enabled. Does not modify the draft.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (ds4.ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return ds4.ToolResult{}, err
			}
			text := func(s string) (ds4.ToolResult, error) {
				return ds4.ToolResult{Parts: []ds4.ContentPart{{Text: s}}}, nil
			}
			data, err := os.ReadFile(draftPath)
			if os.IsNotExist(err) || (err == nil && strings.TrimSpace(string(data)) == "") {
				return text("Preview unavailable: the draft is empty. Create a complete SVG with svg_append first.")
			}
			if err != nil {
				return text(fmt.Sprintf("Error reading draft for preview: %v", err))
			}
			if _, err := inspectSVGDocument(string(data), false); err != nil {
				return text(fmt.Sprintf("Invalid SVG: %v. Use svg_read and the replacement tools to repair the draft before previewing.", err))
			}
			img, err := renderVisualPreview(data)
			if err != nil {
				return text(fmt.Sprintf("Preview rendering failed: %v. Use svg_validate for diagnostics and repair the draft.", err))
			}
			if err := ctx.Err(); err != nil {
				return ds4.ToolResult{}, err
			}
			return ds4.ToolResult{Parts: []ds4.ContentPart{
				{Text: "Current draft.svg rendered on white, with canvas proportions preserved (longest edge 1024 px). Inspect the image for clipping, overlapping labels, readability, spacing, and composition. This is an observation of the existing draft; use targeted replacements if needed."},
				{Image: &img},
			}}, nil
		},
	}
}

// Gate image results on the engine actually loaded for this run, rather than
// the model's filename or the startup options. Keep mixed tool batches ordered.
func executeSVGTools(ctx context.Context, reg *ds4.ToolRegistry, calls []ds4.ToolCall, vision bool) ([]ds4.ChatMessage, error) {
	var results []ds4.ChatMessage
	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if call.Name == svgPreviewName && !vision {
			results = append(results, ds4.ChatMessage{Role: "tool", ToolCallID: call.ID,
				Content: "Preview unavailable: image feedback requires a vision model with its encoder loaded and --visual-review auto or on. Use svg_validate for text diagnostics."})
			continue
		}
		out, err := reg.ExecuteToolCalls(ctx, []ds4.ToolCall{call})
		if err != nil {
			return nil, err
		}
		results = append(results, out...)
	}
	return results, nil
}

func svgToolResultText(msg ds4.ChatMessage) string {
	if len(msg.Parts) == 0 {
		return msg.Content
	}
	var out strings.Builder
	for _, part := range msg.Parts {
		out.WriteString(part.Text)
		if part.Image != nil {
			out.WriteString("\n[preview image]")
		}
	}
	return out.String()
}
