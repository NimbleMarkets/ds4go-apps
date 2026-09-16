package main

import (
	"fmt"
	"math"

	ds4 "github.com/NimbleMarkets/ds4go"
)

const defaultToolRounds = 20
const toolBudgetFeedbackID = "svgpad.tool-budget"

func validateToolRounds(rounds int) error {
	// The driver reserves one additional model turn for the final answer.
	if rounds < 1 || rounds == math.MaxInt {
		return fmt.Errorf("--tool-rounds must be between 1 and %d", math.MaxInt-1)
	}
	return nil
}

// Append budget updates to the conversation, preserving the stable system
// prompt and earlier prefix for session reuse. Mark them as application input
// so saved metadata never mistakes one for the user's drawing request.
func prepareSVGHistory(history []ds4.ChatMessage, round, maxRounds int) []ds4.ChatMessage {
	history = latestVisualImage(history)
	limit := maxRounds - 1
	remaining := max(0, limit-round)
	note := fmt.Sprintf("Application tool budget: %d of %d tool-capable rounds remain in this phase.", remaining, limit)
	if round == 0 {
		note += " This is a new drafting or review phase with a fresh budget. Finish as soon as the drawing meets the request."
	}
	switch {
	case remaining == 0:
		note += " No more tools can execute. Give your final response now without tool calls; clearly state any unfinished work or unchecked edits."
	case remaining <= 3:
		note += " Finish up: prioritize necessary corrections, svg_validate, and a final svg_preview when vision is available. Avoid starting new details. Then give your final response."
	}
	return append(history, ds4.ChatMessage{Role: "user", ToolCallID: toolBudgetFeedbackID, Content: note})
}
