package bubble

import (
	"github.com/NimbleMarkets/ds4go"
)

// Event is the base interface for all observable events during generation.
type Event interface {
	event()
}

// --- Concrete events ---

type TokenEvent struct {
	Text string
}

func (TokenEvent) event() {}

type AssistantMessageEvent struct {
	Message ds4.ChatMessage
}

func (AssistantMessageEvent) event() {}

type ToolCallsEvent struct {
	Calls []ds4.ToolCall
}

func (ToolCallsEvent) event() {}

type ToolResultsEvent struct {
	Results []ds4.ChatMessage // tool role messages
}

func (ToolResultsEvent) event() {}

type RoundStartedEvent struct {
	Round int
}

func (RoundStartedEvent) event() {}

type RoundCompletedEvent struct {
	Round int
}

func (RoundCompletedEvent) event() {}

type ErrorEvent struct {
	Err error
}

func (ErrorEvent) event() {}

// For custom side-effect logging (very useful for cadpad's Lua case)
type LogEvent struct {
	Level   string // "info", "warn", "error", etc.
	Message string
	Fields  map[string]any
}

func (LogEvent) event() {}
