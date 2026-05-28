// Package steerinspect handles core steering data models, timelines,
// and session runner orchestration.
package steerinspect

import (
	"math"
	"sort"
)

// Alternative represents a potential next token choice from the logits.
type Alternative struct {
	TokenID   int     `json:"token_id"`
	TokenText string  `json:"token_text"`
	Logit     float32 `json:"logit"`
	Logprob   float32 `json:"logprob"`
}

// Step captures the state and data of a single generation step.
type Step struct {
	Pos          int             `json:"pos"`
	TokenID      int             `json:"token_id"`
	TokenText    string          `json:"token_text"`
	Alternatives []Alternative   `json:"alternatives"`
	Entropy      float32         `json:"entropy"`
	Margin       float32         `json:"margin"`
	SteerApplied *SteeringConfig `json:"steer_applied,omitempty"`
}

// CalculateEntropyAndMargin computes Shannon entropy and logprob margin over the given alternatives.
// The input alternatives do not need to be pre-sorted.
func CalculateEntropyAndMargin(alts []Alternative) (entropy float32, margin float32) {
	if len(alts) == 0 {
		return 0, 0
	}

	// Make a copy to sort descending by logprob/logit.
	sorted := make([]Alternative, len(alts))
	copy(sorted, alts)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Logprob > sorted[j].Logprob
	})

	var sumP float64
	probs := make([]float64, len(sorted))
	for i, s := range sorted {
		p := math.Exp(float64(s.Logprob))
		probs[i] = p
		sumP += p
	}

	if sumP > 0 {
		var ent float64
		for _, p := range probs {
			np := p / sumP
			if np > 0 {
				ent -= np * math.Log(np)
			}
		}
		entropy = float32(ent)
	}

	if len(sorted) >= 2 {
		margin = sorted[0].Logprob - sorted[1].Logprob
	}

	return entropy, margin
}
