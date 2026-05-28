package steerinspect

// Divergence represents the point where two lanes branch/diverge.
type Divergence struct {
	Pos            int    // Timeline position of divergence
	TokenTextLaneA string // Token text at divergence in lane A
	TokenTextLaneB string // Token text at divergence in lane B
}

// CompareLanes compares two lanes and returns statistics.
type Comparison struct {
	Diverged        bool
	DivergencePoint Divergence
	LaneALen        int
	LaneBLen        int
	SharedPrefixLen int
	AvgEntropyA     float32
	AvgEntropyB     float32
	TotalLogprobA   float32
	TotalLogprobB   float32
}

// Compare analyzes the similarities and differences between two lanes.
func Compare(laneA, laneB *Lane) Comparison {
	comp := Comparison{
		LaneALen: len(laneA.Steps),
		LaneBLen: len(laneB.Steps),
	}

	stepsA := laneA.GetSteps()
	stepsB := laneB.GetSteps()

	// Calculate average entropy and cumulative logprobs
	var sumEntropyA, sumEntropyB float32
	var totalLogprobA, totalLogprobB float32

	for _, s := range stepsA {
		sumEntropyA += s.Entropy
		// Find logprob of the chosen token in Alternatives
		for _, alt := range s.Alternatives {
			if alt.TokenID == s.TokenID {
				totalLogprobA += alt.Logprob
				break
			}
		}
	}
	for _, s := range stepsB {
		sumEntropyB += s.Entropy
		for _, alt := range s.Alternatives {
			if alt.TokenID == s.TokenID {
				totalLogprobB += alt.Logprob
				break
			}
		}
	}

	if len(stepsA) > 0 {
		comp.AvgEntropyA = sumEntropyA / float32(len(stepsA))
		comp.TotalLogprobA = totalLogprobA
	}
	if len(stepsB) > 0 {
		comp.AvgEntropyB = sumEntropyB / float32(len(stepsB))
		comp.TotalLogprobB = totalLogprobB
	}

	// Find the divergence point
	minLen := len(stepsA)
	if len(stepsB) < minLen {
		minLen = len(stepsB)
	}

	sharedCount := 0
	for i := 0; i < minLen; i++ {
		if stepsA[i].TokenID != stepsB[i].TokenID {
			comp.Diverged = true
			comp.DivergencePoint = Divergence{
				Pos:            stepsA[i].Pos,
				TokenTextLaneA: stepsA[i].TokenText,
				TokenTextLaneB: stepsB[i].TokenText,
			}
			break
		}
		sharedCount++
	}

	comp.SharedPrefixLen = sharedCount
	return comp
}
