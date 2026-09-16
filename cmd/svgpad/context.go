package main

import (
	"errors"
	"fmt"
	"os"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
)

const svgMinResponseTokens = 1024
const svgCheckpointID = "svgpad.context-checkpoint"
const svgCorrectionID = "svgpad.syntax-correction"

func contextLimited(err error) bool {
	return errors.Is(err, bubble.ErrContextBudget) || errors.Is(err, ds4.ErrContextFull) ||
		(err != nil && err.Error() == "ds4go: session context full")
}

func svgContextFeedback(u bubble.ContextUsageEvent) string {
	if u.Capacity <= 0 {
		return ""
	}
	note := fmt.Sprintf("Application context budget (before this notice): %d of %d tokens used, %d remain for responses and future tool results, including image tokens. Tool-round budgets do not reset this capacity.", u.PromptTokens, u.Capacity, u.Remaining())
	switch {
	case u.Remaining() <= 2048 || float64(u.PromptTokens)/float64(u.Capacity) >= .9:
		note += " Context is critically low. Finish now: make only essential small fixes, validate if needed, then give a brief final response stating any unfinished work or unchecked edits. Avoid previews and full-file reads."
	case u.Remaining() < svgTurnMaxTokens || float64(u.PromptTokens)/float64(u.Capacity) >= .75:
		note += " Context is running low. Stop adding detail; prioritize closing and validating the SVG and finishing. Use small svg_read ranges and avoid repeated previews."
	default:
		note += " Keep reasoning and tool observations concise; the draft on disk is the source of truth."
	}
	return note
}

// Recover from context pressure without summarizing edit history or copying a
// potentially huge SVG into the new prompt. Preserve user requirements exactly;
// the model can inspect the actual artifact with the existing read/preview tools.
func checkpointSVGHistory(history []ds4.ChatMessage, draftPath string) ([]ds4.ChatMessage, error) {
	data, err := os.ReadFile(draftPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read draft for context recovery: %w", err)
	}
	next := make([]ds4.ChatMessage, 0, len(history))
	for _, msg := range history {
		if msg.Role == "user" && msg.ToolCallID == "" {
			next = append(next, msg)
		}
	}
	note := "Application context checkpoint: the old assistant/tool transcript and review images were removed to free context. The user requests above are preserved. Prior plans, tool results, and review conclusions are no longer available; do not assume checks passed or pending tool calls executed."
	if len(data) > 0 {
		note += fmt.Sprintf(" The existing draft.svg (%d bytes) is preserved on disk. Continue that drawing: first use svg_read with a small line range to inspect it, then validate and fix only what remains. Do not clear or rebuild it. Use svg_preview when needed and vision is available. Treat this checkpoint as continuation, not a new request with an empty draft.", len(data))
	} else {
		note += " There is no saved draft content. Create the drawing from the preserved user requests."
	}
	next = append(next, ds4.ChatMessage{Role: "user", ToolCallID: svgCheckpointID, Content: note})
	return next, nil
}
