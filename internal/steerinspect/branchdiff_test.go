package steerinspect

import (
	"sort"
	"testing"

	"github.com/NimbleMarkets/ntdiff/diff"
)

// fakeLane builds a Lane with the given ID/parent and a deterministic Step
// slice whose Pos values increment from initialPos.
func fakeLane(id, parentID string, parentPos, initialPos int, tokens []int) *Lane {
	l := &Lane{
		ID:         id,
		ParentID:   parentID,
		ParentPos:  parentPos,
		InitialPos: initialPos,
	}
	for i, tok := range tokens {
		l.Steps = append(l.Steps, Step{
			Pos:       initialPos + i,
			TokenID:   tok,
			TokenText: "",
		})
	}
	return l
}

func TestEligiblePairs_AllPairsFromCommonRoot(t *testing.T) {
	root := fakeLane("L1", "", 0, 0, []int{1, 2, 3, 4, 5})
	a := fakeLane("L2", "L1", 3, 3, []int{1, 2, 3, 9, 9})    // forked at Pos 3
	b := fakeLane("L3", "L1", 3, 3, []int{1, 2, 3, 8, 8})    // forked at Pos 3
	c := fakeLane("L4", "L2", 5, 5, []int{1, 2, 3, 9, 9, 7}) // forked from L2 at Pos 5

	lanes := map[string]*Lane{"L1": root, "L2": a, "L3": b, "L4": c}
	pairs := EligiblePairs(lanes)

	// We expect every unique unordered pair: C(4,2) = 6.
	if len(pairs) != 6 {
		t.Fatalf("expected 6 pairs, got %d: %+v", len(pairs), pairs)
	}

	// Canonical ordering: LeftID < RightID.
	for _, p := range pairs {
		if !(p.LeftID < p.RightID) {
			t.Errorf("pair not canonical: %s,%s", p.LeftID, p.RightID)
		}
	}

	// Fork-step expectations:
	//   (L1,L2) forks at Pos 3      (L2's ParentPos)
	//   (L1,L3) forks at Pos 3      (L3's ParentPos)
	//   (L1,L4) forks at Pos 3      (LCA = L2's ParentPos = 3, since L4's chain root is L2)
	//   (L2,L3) forks at Pos 3      (siblings same ParentPos)
	//   (L2,L4) forks at Pos 5      (L4's ParentPos against parent L2)
	//   (L3,L4) forks at Pos 3      (LCA: through L2 and L3 back to L1 fork point)
	wantFork := map[string]int{
		"L1|L2": 3,
		"L1|L3": 3,
		"L1|L4": 3,
		"L2|L3": 3,
		"L2|L4": 5,
		"L3|L4": 3,
	}

	// Sort pairs for stable lookup.
	got := map[string]int{}
	for _, p := range pairs {
		got[p.LeftID+"|"+p.RightID] = p.ForkStep
	}

	keys := make([]string, 0, len(wantFork))
	for k := range wantFork {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if got[k] != wantFork[k] {
			t.Errorf("fork mismatch for %s: got %d, want %d", k, got[k], wantFork[k])
		}
	}
}

func TestEligiblePairs_EmptyOrSingleReturnsNoPairs(t *testing.T) {
	if got := EligiblePairs(nil); len(got) != 0 {
		t.Fatalf("nil lanes: expected 0 pairs, got %d", len(got))
	}
	root := fakeLane("L1", "", 0, 0, []int{1, 2})
	if got := EligiblePairs(map[string]*Lane{"L1": root}); len(got) != 0 {
		t.Fatalf("single lane: expected 0 pairs, got %d", len(got))
	}
}

func TestBuildRichDiff_CollapsedSharedPrefix(t *testing.T) {
	// Both lanes share 10 identical steps (Pos 0..9), then diverge.
	// Right uses initialPos=0 so the shared steps occupy Pos 0..9 in both
	// lanes — required for absolute-Pos fork semantics to agree.
	common := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	left := fakeLane("L1", "", 0, 0, append(append([]int{}, common...), 100, 101))
	right := fakeLane("L2", "L1", 10, 0, append(append([]int{}, common...), 200, 201))

	got := BuildRichDiff(left, right, 10, true)
	if len(got.Lines) != 1+2 { // 1 collapsed + 2 divergent rows
		t.Fatalf("expected 3 lines (1 collapsed + 2 changed), got %d", len(got.Lines))
	}
	if got.Lines[0].Status != diff.Collapsed {
		t.Fatalf("expected first line Collapsed, got %v", got.Lines[0].Status)
	}
	cm, ok := got.Lines[0].Meta.(CollapsedMeta)
	if !ok || cm.Count != 10 {
		t.Fatalf("expected CollapsedMeta{Count:10}, got %#v", got.Lines[0].Meta)
	}
	if got.ForkIndex != 1 {
		t.Fatalf("expected ForkIndex=1 (after collapsed row), got %d", got.ForkIndex)
	}
	if got.Lines[1].Status != diff.Changed || got.Lines[2].Status != diff.Changed {
		t.Fatalf("expected post-fork rows Changed, got %v / %v", got.Lines[1].Status, got.Lines[2].Status)
	}
}

func TestBuildRichDiff_ExpandedSharedPrefix(t *testing.T) {
	common := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	left := fakeLane("L1", "", 0, 0, append(append([]int{}, common...), 100, 101))
	right := fakeLane("L2", "L1", 10, 0, append(append([]int{}, common...), 200, 201))

	got := BuildRichDiff(left, right, 10, false)
	if len(got.Lines) != 10+2 {
		t.Fatalf("expected 12 lines (10 shared + 2 changed), got %d", len(got.Lines))
	}
	for i := 0; i < 10; i++ {
		if got.Lines[i].Status != diff.Shared {
			t.Fatalf("line %d: expected Shared, got %v", i, got.Lines[i].Status)
		}
	}
	if got.ForkIndex != 10 {
		t.Fatalf("expected ForkIndex=10 (after 10 shared rows), got %d", got.ForkIndex)
	}
}

func TestBuildRichDiff_ForkAlignmentUnevenPostFork(t *testing.T) {
	// Parent has 20 steps (Pos 0..19). Child branched at Pos 12 then generated
	// 5 of its own (Pos 12..16). Expect 12 shared + max(8,5)=8 post-fork.
	parentToks := []int{}
	for i := 0; i < 20; i++ {
		parentToks = append(parentToks, 1000+i)
	}
	childToks := append([]int{}, parentToks[:12]...)
	childToks = append(childToks, 7000, 7001, 7002, 7003, 7004)

	parent := fakeLane("L1", "", 0, 0, parentToks)
	child := fakeLane("L2", "L1", 12, 12, childToks)
	// Child Step.Pos values start at 12 thanks to fakeLane's initialPos arg —
	// fix child to look correct: copy parent steps[0..12] then append child-only.
	child.Steps = nil
	for i := 0; i < 12; i++ {
		child.Steps = append(child.Steps, parent.Steps[i])
	}
	for i := 0; i < 5; i++ {
		child.Steps = append(child.Steps, Step{Pos: 12 + i, TokenID: 7000 + i})
	}

	got := BuildRichDiff(parent, child, 12, true)
	// 1 collapsed + 8 post-fork rows.
	if len(got.Lines) != 1+8 {
		t.Fatalf("expected 9 lines, got %d", len(got.Lines))
	}
	// Rows 1..5 should be Changed (both sides have content).
	for i := 1; i <= 5; i++ {
		if got.Lines[i].Status != diff.Changed {
			t.Errorf("line %d: expected Changed, got %v", i, got.Lines[i].Status)
		}
	}
	// Rows 6..8 should be LeftOnly (parent extends past child).
	for i := 6; i <= 8; i++ {
		if got.Lines[i].Status != diff.LeftOnly {
			t.Errorf("line %d: expected LeftOnly, got %v", i, got.Lines[i].Status)
		}
	}
}

func TestBuildRichDiff_RankInOtherSide(t *testing.T) {
	// Left chose token 42; right's top-3 alternatives include 42 at rank 3.
	// Right chose token 99; left's top-3 alternatives do NOT include 99.
	left := &Lane{ID: "L1"}
	left.Steps = []Step{
		{Pos: 0, TokenID: 42, TokenText: "L",
			Alternatives: []Alternative{
				{TokenID: 5, Logprob: -1.0}, {TokenID: 7, Logprob: -2.0}, {TokenID: 8, Logprob: -3.0},
			}},
	}
	right := &Lane{ID: "L2", ParentID: "L1", ParentPos: 0}
	right.Steps = []Step{
		{Pos: 0, TokenID: 99, TokenText: "R",
			Alternatives: []Alternative{
				{TokenID: 5, Logprob: -1.0}, {TokenID: 6, Logprob: -2.0}, {TokenID: 42, Logprob: -3.0},
			}},
	}

	got := BuildRichDiff(left, right, 0, true)
	if len(got.Lines) != 1 {
		t.Fatalf("expected 1 post-fork line, got %d", len(got.Lines))
	}
	meta, ok := got.Lines[0].Meta.(StepMeta)
	if !ok {
		t.Fatalf("expected StepMeta, got %#v", got.Lines[0].Meta)
	}
	if meta.LeftRankInRightTopK != 3 {
		t.Errorf("LeftRankInRightTopK: got %d, want 3", meta.LeftRankInRightTopK)
	}
	if meta.RightRankInLeftTopK != -1 {
		t.Errorf("RightRankInLeftTopK: got %d, want -1", meta.RightRankInLeftTopK)
	}
}

func TestBuildRichDiff_SteeringChangedFlags(t *testing.T) {
	cfgA := &SteeringConfig{Vector: "v1", FFNScale: 1}
	cfgB := &SteeringConfig{Vector: "v1", FFNScale: 2}

	left := &Lane{ID: "L1"}
	left.Steps = []Step{
		{Pos: 0, TokenID: 1, SteerApplied: cfgA},
		{Pos: 1, TokenID: 2, SteerApplied: cfgA}, // unchanged
		{Pos: 2, TokenID: 3, SteerApplied: cfgB}, // changed
	}
	right := &Lane{ID: "L2", ParentID: "L1", ParentPos: 0}
	right.Steps = []Step{
		{Pos: 0, TokenID: 9, SteerApplied: nil},
		{Pos: 1, TokenID: 8, SteerApplied: cfgA}, // changed (nil → cfgA)
		{Pos: 2, TokenID: 7, SteerApplied: cfgA}, // unchanged
	}

	got := BuildRichDiff(left, right, 0, true)
	if len(got.Lines) != 3 {
		t.Fatalf("expected 3 post-fork rows, got %d", len(got.Lines))
	}
	type expect struct{ left, right bool }
	wants := []expect{
		{left: true, right: true},  // row 0: first step → both transitions from nil
		{left: false, right: true}, // row 1: left unchanged, right transitioned
		{left: true, right: false}, // row 2: left transitioned, right unchanged
	}
	for i, w := range wants {
		meta := got.Lines[i].Meta.(StepMeta)
		if meta.LeftSteeringChanged != w.left {
			t.Errorf("row %d: LeftSteeringChanged = %v, want %v", i, meta.LeftSteeringChanged, w.left)
		}
		if meta.RightSteeringChanged != w.right {
			t.Errorf("row %d: RightSteeringChanged = %v, want %v", i, meta.RightSteeringChanged, w.right)
		}
	}
}

func TestBuildRichDiff_BothSidesEndedAtDifferentLengths(t *testing.T) {
	// Left ends at Pos 14, Right at Pos 19. Shared count = 10.
	leftToks := make([]int, 15)
	rightToks := make([]int, 20)
	for i := 0; i < 10; i++ {
		leftToks[i] = i
		rightToks[i] = i
	}
	for i := 10; i < 15; i++ {
		leftToks[i] = 1000 + i
	}
	for i := 10; i < 20; i++ {
		rightToks[i] = 2000 + i
	}

	left := fakeLane("L1", "", 0, 0, leftToks)
	right := fakeLane("L2", "L1", 10, 10, rightToks)
	// Make right's first 10 Steps mirror left's (Pos values must align).
	for i := 0; i < 10; i++ {
		right.Steps[i] = left.Steps[i]
	}

	got := BuildRichDiff(left, right, 10, true)
	// 1 collapsed + 10 post-fork rows (max of 5 left-only post-fork, 10 right post-fork)
	if len(got.Lines) != 1+10 {
		t.Fatalf("expected 11 lines, got %d", len(got.Lines))
	}
	// Rows 1..5 should be Changed (both have content for these positions).
	for i := 1; i <= 5; i++ {
		if got.Lines[i].Status != diff.Changed {
			t.Errorf("row %d: expected Changed, got %v", i, got.Lines[i].Status)
		}
	}
	// Rows 6..10 should be RightOnly (left has ended).
	for i := 6; i <= 10; i++ {
		if got.Lines[i].Status != diff.RightOnly {
			t.Errorf("row %d: expected RightOnly, got %v", i, got.Lines[i].Status)
		}
	}
}
