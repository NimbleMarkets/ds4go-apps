package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/memory"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
)

// finishRun records durable outcomes without spending any model/tool rounds.
// A hard budget stop gets a factual host summary, never the model's unsupported
// claims accompanying rejected calls. Ordinary failures remain visible.
func finishRun(ctx context.Context, res bubble.RunResult, runErr error, state *tools.State, mem *memory.Memory) doneMsg {
	msg := doneMsg{result: res, err: runErr}
	limited := res.BudgetExhausted || errors.Is(runErr, bubble.ErrMaxRounds)
	if limited {
		msg.result.BudgetExhausted = true
		snap := state.Snapshot()
		note := fmt.Sprintf("Tool round limit reached after %d executed rounds. Current shader %q (revision %d) and live controls are retained.", res.ToolRounds, clipped(snap.Source.Name, 256), snap.Revision)
		if errors.Is(runErr, bubble.ErrMaxRounds) {
			var pending []string
			for _, call := range res.Assistant.ToolCalls {
				pending = append(pending, call.Name)
			}
			note += " Not executed: " + clipped(strings.Join(pending, ", "), 800) + "."
			msg.err = nil // handled stop; not a successful completion of the request
		}
		if runErr != nil && !errors.Is(runErr, bubble.ErrMaxRounds) {
			note += " Final summary could not complete: " + clipped(runErr.Error(), 400) + "."
		}
		note += " Further work may remain; send another prompt to continue from the current shader."
		if runErr != nil || strings.TrimSpace(res.Assistant.Content) == "" || res.Assistant.MalformedReason != "" {
			msg.result.Assistant = ds4.ChatMessage{Role: "assistant", Content: note}
			msg.result.History = append(append([]ds4.ChatMessage(nil), res.History...), msg.result.Assistant)
		}
		msg.notice = "Round limit reached · work retained · send a prompt to continue"
	}
	// Cancellation should not start filesystem work after the user asks to stop.
	if mem == nil || ctx.Err() != nil {
		return msg
	}
	result := "Completed. " + clipped(msg.result.Assistant.Content, 2000)
	if limited {
		result = "Round limit reached; completion is not guaranteed. " + clipped(msg.result.Assistant.Content, 2000)
	} else if runErr != nil {
		result = "Stopped: " + clipped(runErr.Error(), 1000) + ". Live shader edits are retained; inspect trip_describe before continuing."
	}
	var saveErr error
	if limited {
		// Include current request, authoritative state and recent observations,
		// not raw reasoning or shader dumps. Rejected observations say NOT EXECUTED.
		request := ""
		var observations []string
		for _, entry := range msg.result.History {
			if entry.Role == "user" && entry.ToolCallID == "" {
				request = messageText(entry)
				observations = nil
			}
			if entry.Role == "tool" {
				observations = append(observations, clipped(messageText(entry), 240))
			}
		}
		snap := state.Snapshot()
		note := fmt.Sprintf("Round-limit checkpoint. %s\nCurrent shader %q, revision %d, controls %v. Live state/gallery are authoritative; do not replay completed operations.\nLatest request:\n%s\nRecent tool observations (including failures or rejected calls):\n%s",
			result, clipped(snap.Source.Name, 256), snap.Revision, snap.Named, clipped(request, 4000), strings.Join(observations[max(0, len(observations)-8):], "\n"))
		saveErr = mem.Set(ctx, "checkpoint", note)
		if saveErr == nil {
			msg.notice = "Round limit reached · checkpoint saved · send a prompt to continue"
		}
	}
	if err := mem.Set(ctx, "last-result", result); err != nil {
		saveErr = errors.Join(saveErr, err)
	}
	if saveErr != nil {
		msg.err = errors.Join(msg.err, fmt.Errorf("could not save run notes: %w", saveErr))
	}
	return msg
}
