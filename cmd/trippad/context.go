package main

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/memory"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
)

const memoryID = "trippad.memory"
const checkpointID = "trippad.checkpoint"

func (m *model) contextLabel() string {
	if m.contextUsage.Capacity > 0 {
		return fmt.Sprintf("prompt %d/%d tokens", m.contextUsage.PromptTokens, m.contextUsage.Capacity)
	}
	if !m.noEngine && m.app != nil && m.app.Flags != nil {
		return fmt.Sprintf("context %d tokens configured", m.app.Flags.Ctx)
	}
	return ""
}

func (m *model) contextDetails() string {
	status := "Prompt usage is recalculated at the next request."
	if m.contextUsage.Capacity > 0 {
		status = fmt.Sprintf("Last measured prompt: %d / %d tokens; %d remain for replies and tool results.", m.contextUsage.PromptTokens, m.contextUsage.Capacity, m.contextUsage.Remaining())
	}
	return m.contextLabel() + "\n" + status + fmt.Sprintf("\nThe allocation is set by --ctx, not the model's maximum. Default: %d (%dK).", defaultContextTokens, defaultContextTokens/1024) + "\nPrompt usage includes system instructions, tools, history, memory notes and image tokens.\nRestart with --ctx N to change the allocation; larger windows use more memory."
}

func clipped(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "\n[excerpt; remaining text omitted]"
}

func messageText(m ds4.ChatMessage) string {
	if len(m.Parts) == 0 {
		return m.Content
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Text != "" {
			b.WriteString(p.Text + "\n")
		}
		if p.Image != nil {
			b.WriteString("[preview image]\n")
		}
	}
	return b.String()
}

// Reduce repeated payloads without breaking call/result IDs or modifying the
// original messages. Streamed reasoning remains available in the inspector.
func leanHistory(history []ds4.ChatMessage) []ds4.ChatMessage {
	latestImage := -1
	for i, m := range history {
		if m.Role == "tool" {
			for _, p := range m.Parts {
				if p.Image != nil {
					latestImage = i
				}
			}
		}
	}
	out := make([]ds4.ChatMessage, 0, len(history))
	for i, m := range history {
		if m.ToolCallID == "pad.tool-budget" || m.ToolCallID == bubble.ContextFeedbackID {
			continue
		}
		m.ReasoningContent = ""
		if m.Role == "tool" {
			// Bound compiler dumps, but leave successful source reads intact.
			bound := func(s string) string {
				if strings.Contains(s, "ERROR:") || strings.HasPrefix(s, "Error") {
					return clipped(s, 4000)
				}
				return s
			}
			m.Content = bound(m.Content)
			m.Parts = append([]ds4.ContentPart(nil), m.Parts...)
			for j, p := range m.Parts {
				p.Text = bound(p.Text)
				if p.Image != nil && i != latestImage {
					p.Image = nil
					p.Text += "[older preview omitted; request trip_preview for the current frame]"
				}
				m.Parts[j] = p
			}
		}
		out = append(out, m)
	}
	return out
}

// compactContext records excerpts and the authoritative workspace identity,
// retaining the latest user request verbatim and the newest assistant/tool
// group. This is a bounded checkpoint, not a claim to summarize every detail.
func compactContext(ctx context.Context, history []ds4.ChatMessage, state *tools.State, mem *memory.Memory) ([]ds4.ChatMessage, error) {
	latest := -1
	for i, m := range history {
		if m.Role == "user" && m.ToolCallID == "" {
			latest = i
		}
	}
	if latest < 0 {
		return history, nil
	}
	lastGroup := -1
	for i := latest + 1; i < len(history); i++ {
		if history[i].Role == "assistant" {
			lastGroup = i
		}
	}
	var prior, outcomes strings.Builder
	for i, m := range history {
		if m.ToolCallID == checkpointID {
			prior.WriteString(clipped(m.Content, 1800) + "\n")
		}
		if m.Role == "user" && m.ToolCallID == "" && i != latest {
			prior.WriteString(clipped(messageText(m), 800) + "\n")
		}
		if m.Role == "tool" {
			outcomes.WriteString(m.ToolCallID + ": " + clipped(messageText(m), 220) + "\n")
		}
	}
	tail := outcomes.String()
	if len(tail) > 2000 {
		tail = strings.ToValidUTF8(tail[len(tail)-2000:], "�")
	}
	snap := state.Snapshot()
	note := fmt.Sprintf("Context checkpoint: earlier transcript was reduced to excerpts; some details are omitted. Current shader %q, revision %d, parameters %v. Live state and gallery are authoritative; inspect trip_describe before editing. Completed operations must not be repeated solely because history was compacted. The user message below is the active request.\nPrior context excerpts (not new requests):\n%s\nRecent tool observations (may include failures):\n%s", snap.Source.Name, snap.Revision, snap.Named, clipped(prior.String(), 2400), tail)
	out := []ds4.ChatMessage{{Role: "user", ToolCallID: checkpointID, Content: note}}
	if mem != nil {
		if err := mem.Set(ctx, "checkpoint", note); err != nil {
			return nil, fmt.Errorf("save context checkpoint: %w", err)
		}
		brief, err := mem.Brief(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, ds4.ChatMessage{Role: "user", ToolCallID: memoryID, Content: brief})
	}
	out = append(out, history[latest])
	if lastGroup > latest {
		out = append(out, leanHistory(history[lastGroup:])...)
	}
	return out, nil
}

func memoryHistory(ctx context.Context, history []ds4.ChatMessage, mem *memory.Memory) ([]ds4.ChatMessage, error) {
	if mem == nil {
		return history, nil
	}
	brief, err := mem.Brief(ctx)
	if err != nil {
		return nil, err
	}
	out := []ds4.ChatMessage{{Role: "user", ToolCallID: memoryID, Content: brief}}
	for _, m := range history {
		if m.ToolCallID != memoryID {
			out = append(out, m)
		}
	}
	return out, nil
}

type memoryViewMsg struct {
	text string
	err  error
}
type compactedMsg struct {
	history []ds4.ChatMessage
	err     error
}
