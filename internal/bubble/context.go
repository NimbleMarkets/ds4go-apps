package bubble

import "errors"

// ErrContextBudget means the next prompt leaves too little response space.
// No generation or tool execution occurs for that round; History is retained.
var ErrContextBudget = errors.New("bubble: insufficient context remaining for another turn")

// ContextUsageEvent measures the rendered next prompt, including system text,
// tool schemas, history, and image token positions. It is not the previous
// session's KV-cache position, which may change when the prompt is synced.
type ContextUsageEvent struct {
	PromptTokens int
	Capacity     int
}

func (ContextUsageEvent) event() {}

func (u ContextUsageEvent) Remaining() int { return max(0, u.Capacity-u.PromptTokens) }

// ContextFeedbackID marks host guidance so apps can exclude it from user intent.
const ContextFeedbackID = "bubble.context-budget"
