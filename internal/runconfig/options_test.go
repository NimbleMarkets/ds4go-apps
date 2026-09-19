package runconfig

import (
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"math"
	"strings"
	"testing"
)

func TestLimitsAndFeedback(t *testing.T) {
	o := Options{Temperature: .7, TopP: .95, ToolRounds: 20, Seed: 42}
	if o.Validate() != nil || o.EffectiveSeed() != 42 {
		t.Fatal("valid options rejected")
	}
	for _, bad := range []Options{{Temperature: float32(math.NaN()), ToolRounds: 1}, {ToolRounds: 0}, {Temperature: 3, ToolRounds: 1}, {TopP: 2, ToolRounds: 1}} {
		if bad.Validate() == nil {
			t.Fatal("invalid options accepted")
		}
	}
	original := []ds4.ChatMessage{{Role: "user", Content: "make a box"}}
	next := PrepareHistory(original, 20, 21)
	if len(original) != 1 || len(next) != 2 || !strings.Contains(next[1].Content, "No more tools") {
		t.Fatal("missing final round guidance")
	}
	if !strings.Contains(ContextFeedback(bubble.ContextUsageEvent{PromptTokens: 3900, Capacity: 4096}), "critically low") {
		t.Fatal("missing context warning")
	}
}
