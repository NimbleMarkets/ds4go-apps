// Package steerinspect — branchdiff.go: utilities for computing diffs between
// any two lanes that share a common ancestral root.
package steerinspect

import (
	"math"
	"sort"

	"github.com/NimbleMarkets/ntdiff/diff"
)

// EligiblePair is a canonicalised (LeftID < RightID) pair of lanes together
// with the absolute token position at which they last shared history.
type EligiblePair struct {
	LeftID     string
	RightID    string
	LeftLabel  string
	RightLabel string
	ForkStep   int // absolute timeline Pos at which the lanes last shared history
}

// EligiblePairs returns every unordered pair of lanes in the given map,
// computing the absolute timeline position at which each pair last shared
// history. Because all lanes ultimately descend from the runner's root lane,
// every pair is considered eligible.
func EligiblePairs(lanes map[string]*Lane) []EligiblePair {
	ids := make([]string, 0, len(lanes))
	for id := range lanes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []EligiblePair
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			leftID, rightID := ids[i], ids[j]
			fork := forkStep(lanes, leftID, rightID)
			out = append(out, EligiblePair{
				LeftID:     leftID,
				RightID:    rightID,
				LeftLabel:  leftID,
				RightLabel: rightID,
				ForkStep:   fork,
			})
		}
	}
	return out
}

// forkStep walks each lane's ancestral chain back to the root lane (the one
// with empty ParentID) and returns the absolute Pos where the two chains last
// agreed. The chains agree at every position covered by their shared ancestor
// segment(s), so the fork point is the smaller of:
//   - the deepest ancestor common to both chains, capped by
//   - each lane's own ParentPos (the point at which it diverged from its parent).
//
// For two lanes that share an immediate ancestor, this is the smaller of their
// two ParentPos values. For a parent/child pair, it is the child's ParentPos.
// For a lane paired with itself's ancestor through a chain, it is the
// shallowest ParentPos along the path between them.
func forkStep(lanes map[string]*Lane, leftID, rightID string) int {
	// Ancestor chain for a lane: list of (laneID, divergenceFromParentPos)
	// starting at the lane itself and walking up via ParentID. The root lane
	// has ParentID == "" and ParentPos == 0; we treat its entry as the root
	// marker.
	chain := func(start string) []ancestor {
		var out []ancestor
		cur := start
		for cur != "" {
			l, ok := lanes[cur]
			if !ok {
				break
			}
			out = append(out, ancestor{ID: l.ID, ParentPos: l.ParentPos, ParentID: l.ParentID})
			cur = l.ParentID
		}
		return out
	}

	leftChain := chain(leftID)
	rightChain := chain(rightID)

	// Build a set of ancestor IDs in the left chain mapped to the cumulative
	// minimum ParentPos along the path from leftID to that ancestor. The
	// minimum captures the tightest constraint on shared history.
	// We record leftMin[a.ID] = cur BEFORE applying a's own ParentPos, so
	// that the leftID node itself gets MaxInt32 (unconstrained) and each
	// ancestor gets the constraint from nodes between leftID and that ancestor.
	leftMin := map[string]int{}
	cur := math.MaxInt32
	for _, a := range leftChain {
		leftMin[a.ID] = cur
		if a.ParentID != "" {
			if a.ParentPos < cur {
				cur = a.ParentPos
			}
		}
	}

	// Walk the right chain and find the first ancestor that appears in the
	// left chain; combine the minimum ParentPos seen along the right path with
	// the minimum recorded for that ancestor in leftMin. We check leftMin
	// BEFORE updating curR with the current node's own ParentPos, so that
	// the rightID node itself is checked with curR=MaxInt32 (unconstrained)
	// and each ancestor is checked with the constraint from nodes between
	// rightID and that ancestor.
	curR := math.MaxInt32
	for _, a := range rightChain {
		if lm, ok := leftMin[a.ID]; ok {
			fork := lm
			if curR < fork {
				fork = curR
			}
			if fork == math.MaxInt32 {
				fork = 0
			}
			return fork
		}
		if a.ParentID != "" {
			if a.ParentPos < curR {
				curR = a.ParentPos
			}
		}
	}
	return 0
}

type ancestor struct {
	ID        string
	ParentID  string
	ParentPos int
}

// findStepIndex returns the slice index of the Step with the given absolute
// Pos value, or len(steps) if no Step has Pos >= target (meaning the lane
// ended before reaching target).
func findStepIndex(steps []Step, targetPos int) int {
	for i, s := range steps {
		if s.Pos >= targetPos {
			return i
		}
	}
	return len(steps)
}

// BuildRichDiff produces a ntdiff.RichDiff for the given lane pair. When
// collapsed is true and there is a non-empty shared prefix, that prefix is
// represented by a single Collapsed line; otherwise individual Shared lines
// are emitted.
//
// fork is the absolute timeline Pos at which the lanes diverged; it is
// converted internally to a per-lane step index via findStepIndex. The shared
// step count is the minimum of the two per-lane indices, capped by each lane's
// actual length.
func BuildRichDiff(left, right *Lane, fork int, collapsed bool) diff.RichDiff {
	leftSteps := left.GetSteps()
	rightSteps := right.GetSteps()
	// Convert the absolute Pos fork point to per-lane step indices, then take
	// the minimum so we only treat steps as shared if both lanes agree.
	leftFork := findStepIndex(leftSteps, fork)
	rightFork := findStepIndex(rightSteps, fork)
	sharedCount := leftFork
	if rightFork < sharedCount {
		sharedCount = rightFork
	}

	var lines []diff.RichLine

	if sharedCount > 0 && collapsed {
		lines = append(lines, diff.RichLine{
			Status: diff.Collapsed,
			Meta:   CollapsedMeta{Count: sharedCount},
		})
	} else if !collapsed {
		for i := 0; i < sharedCount; i++ {
			s := leftSteps[i]
			lp := chosenLogprob(s)
			lines = append(lines, diff.RichLine{
				LeftText:  s.TokenText,
				RightText: s.TokenText,
				Status:    diff.Shared,
				Meta: StepMeta{
					LeftToken:    s.TokenID,
					RightToken:   s.TokenID,
					LeftLogprob:  lp,
					RightLogprob: lp,
				},
			})
		}
	}

	forkIndex := len(lines)

	// Use a non-nil sentinel so that the very first post-fork step always
	// registers as a steering change (nil SteerApplied on step 0 still
	// transitions from "no prior context" to an explicit nil/absent config).
	sentinel := &SteeringConfig{}
	prevLeftSteer, prevRightSteer := sentinel, sentinel

	maxLen := len(leftSteps)
	if len(rightSteps) > maxLen {
		maxLen = len(rightSteps)
	}

	for i := sharedCount; i < maxLen; i++ {
		var l, r *Step
		if i < len(leftSteps) {
			l = &leftSteps[i]
		}
		if i < len(rightSteps) {
			r = &rightSteps[i]
		}

		status := diff.Changed
		switch {
		case l == nil:
			status = diff.RightOnly
		case r == nil:
			status = diff.LeftOnly
		}

		meta := StepMeta{
			LeftRankInRightTopK: -1,
			RightRankInLeftTopK: -1,
			LeftLogprob:         -math.MaxFloat32,
			RightLogprob:        -math.MaxFloat32,
		}

		if l != nil {
			meta.LeftToken = l.TokenID
			meta.LeftLogprob = chosenLogprob(*l)
			meta.LeftSteeringChanged = steeringChanged(l.SteerApplied, prevLeftSteer)
			prevLeftSteer = l.SteerApplied
		}
		if r != nil {
			meta.RightToken = r.TokenID
			meta.RightLogprob = chosenLogprob(*r)
			meta.RightSteeringChanged = steeringChanged(r.SteerApplied, prevRightSteer)
			prevRightSteer = r.SteerApplied
		}
		if l != nil && r != nil {
			meta.LeftRankInRightTopK = rankInOtherSide(l.TokenID, r.Alternatives)
			meta.RightRankInLeftTopK = rankInOtherSide(r.TokenID, l.Alternatives)
		}

		lines = append(lines, diff.RichLine{
			LeftText:  textOrEOF(l),
			RightText: textOrEOF(r),
			Status:    status,
			Meta:      meta,
		})
	}

	return diff.RichDiff{
		Lines:      lines,
		ForkIndex:  forkIndex,
		LeftLabel:  left.ID,
		RightLabel: right.ID,
	}
}

// StepMeta is attached to non-collapsed RichLines emitted by BuildRichDiff.
type StepMeta struct {
	LeftToken            int
	RightToken           int
	LeftLogprob          float32 // -math.MaxFloat32 when chosen token absent from captured alternatives
	RightLogprob         float32
	LeftRankInRightTopK  int // -1 when outside the captured top-K
	RightRankInLeftTopK  int
	LeftSteeringChanged  bool
	RightSteeringChanged bool
}

// CollapsedMeta is attached to the synthetic collapsed-prefix RichLine and
// satisfies diff.CollapsedCounter so ntdiff can render the count without
// depending on this package.
type CollapsedMeta struct {
	Count int
}

// CollapsedRowCount implements diff.CollapsedCounter.
func (c CollapsedMeta) CollapsedRowCount() int { return c.Count }

func chosenLogprob(s Step) float32 {
	for _, a := range s.Alternatives {
		if a.TokenID == s.TokenID {
			return a.Logprob
		}
	}
	return -math.MaxFloat32
}

func rankInOtherSide(tokenID int, alts []Alternative) int {
	for i, a := range alts {
		if a.TokenID == tokenID {
			return i + 1 // 1-based rank
		}
	}
	return -1
}

func steeringChanged(now, prev *SteeringConfig) bool {
	if now == nil && prev == nil {
		return false
	}
	if (now == nil) != (prev == nil) {
		return true
	}
	return *now != *prev
}

func textOrEOF(s *Step) string {
	if s == nil {
		return "␄"
	}
	return s.TokenText
}
