package steertui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/steerinspect"
)

// buildTwoSiblingLanes returns two lanes sharing a 5-step prefix and divergent
// content after Pos 5.
func buildTwoSiblingLanes(t *testing.T) (*steerinspect.Lane, *steerinspect.Lane, int) {
	t.Helper()
	left := &steerinspect.Lane{ID: "L1", InitialPos: 0}
	right := &steerinspect.Lane{ID: "L2", ParentID: "L1", ParentPos: 5, InitialPos: 5}
	for i := 0; i < 5; i++ {
		s := steerinspect.Step{Pos: i, TokenID: i, TokenText: "s"}
		left.Steps = append(left.Steps, s)
		right.Steps = append(right.Steps, s)
	}
	for i := 5; i < 8; i++ {
		left.Steps = append(left.Steps, steerinspect.Step{Pos: i, TokenID: 100 + i, TokenText: "L"})
		right.Steps = append(right.Steps, steerinspect.Step{Pos: i, TokenID: 200 + i, TokenText: "R"})
	}
	return left, right, 5
}

func TestDiffView_OpenBuildsRichDiff(t *testing.T) {
	d := NewDiffView()
	left, right, fork := buildTwoSiblingLanes(t)
	d.Open(left, right, fork)
	d.SetSize(80, 20)
	if d.LineCount() != 1+3 {
		t.Fatalf("expected 4 lines (collapsed + 3 changed), got %d", d.LineCount())
	}
}

func TestDiffView_ToggleCollapsedRebuilds(t *testing.T) {
	d := NewDiffView()
	left, right, fork := buildTwoSiblingLanes(t)
	d.Open(left, right, fork)
	d.SetSize(80, 20)

	before := d.LineCount()
	d.ToggleCollapsed()
	after := d.LineCount()
	if after != before-1+5 {
		t.Fatalf("expected %d lines after expand, got %d (was %d)", before-1+5, after, before)
	}
	d.ToggleCollapsed()
	if d.LineCount() != before {
		t.Fatalf("expected %d lines after collapse-again, got %d", before, d.LineCount())
	}
}

func TestDiffView_MetaRendererFormatsBadge(t *testing.T) {
	d := NewDiffView()
	left, right, fork := buildTwoSiblingLanes(t)
	d.Open(left, right, fork)
	d.SetSize(120, 20)
	out := d.View()
	// First post-fork row should carry a rank/Δlp badge with "r:" prefix.
	if !strings.Contains(out, "r:") {
		t.Fatalf("expected diff view to include rank meta badge, got:\n%s", out)
	}
}

func TestRenderDiffPicker_ListsEligiblePairs(t *testing.T) {
	left, right, _ := buildTwoSiblingLanes(t)
	lanes := map[string]*steerinspect.Lane{left.ID: left, right.ID: right}
	out := RenderDiffPicker(80, 20, steerinspect.EligiblePairs(lanes), 0)
	if !strings.Contains(out, "L1") || !strings.Contains(out, "L2") {
		t.Fatalf("expected picker to list L1 and L2, got:\n%s", out)
	}
}

func TestRenderDiffPicker_EmptyState(t *testing.T) {
	out := RenderDiffPicker(80, 20, nil, 0)
	if !strings.Contains(out, "No eligible") {
		t.Fatalf("expected empty-state message, got:\n%s", out)
	}
}

func TestModel_KeyC_OpensPickerWhenEligiblePairsExist(t *testing.T) {
	m := &Model{
		Runner:            &steerinspect.Runner{Lanes: map[string]*steerinspect.Lane{}},
		PromptInitialized: true,
	}
	left, right, _ := buildTwoSiblingLanes(t)
	m.Runner.Lanes[left.ID] = left
	m.Runner.Lanes[right.ID] = right
	m.LaneIDs = []string{left.ID, right.ID}

	_, _ = m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if !m.DiffPickerOpen {
		t.Fatalf("expected DiffPickerOpen=true after pressing c")
	}
}

func TestModel_KeyC_SingleLaneShowsToast(t *testing.T) {
	left := &steerinspect.Lane{ID: "L1"}
	m := &Model{
		Runner:            &steerinspect.Runner{Lanes: map[string]*steerinspect.Lane{"L1": left}},
		PromptInitialized: true,
		LaneIDs:           []string{"L1"},
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if m.DiffPickerOpen {
		t.Fatalf("expected picker NOT opened with single lane")
	}
	if !strings.Contains(m.StatusMsg, "Need at least two lanes") {
		t.Fatalf("expected status toast, got %q", m.StatusMsg)
	}
}

func TestModel_PickerEnter_EntersViewDiff(t *testing.T) {
	m := &Model{
		Runner:            &steerinspect.Runner{Lanes: map[string]*steerinspect.Lane{}},
		PromptInitialized: true,
	}
	left, right, _ := buildTwoSiblingLanes(t)
	m.Runner.Lanes[left.ID] = left
	m.Runner.Lanes[right.ID] = right
	m.LaneIDs = []string{left.ID, right.ID}

	_, _ = m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.Mode != ViewDiff {
		t.Fatalf("expected Mode=ViewDiff after picker enter, got %v", m.Mode)
	}
	if m.DiffPickerOpen {
		t.Fatalf("expected picker closed after enter")
	}
	if m.DiffView == nil {
		t.Fatalf("expected DiffView non-nil after picker enter")
	}
}

func TestModel_EscExitsViewDiff(t *testing.T) {
	m := &Model{
		Runner:            &steerinspect.Runner{Lanes: map[string]*steerinspect.Lane{}},
		PromptInitialized: true,
		Mode:              ViewDiff,
		DiffView:          NewDiffView(),
	}
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.Mode != ViewNormal {
		t.Fatalf("expected Mode=ViewNormal after esc, got %v", m.Mode)
	}
}

func TestModel_ViewDiff_RendersDiffView(t *testing.T) {
	m := &Model{
		Runner:            &steerinspect.Runner{Lanes: map[string]*steerinspect.Lane{}},
		PromptInitialized: true,
		Width:             100,
		Height:            30,
	}
	left, right, fork := buildTwoSiblingLanes(t)
	m.Runner.Lanes[left.ID] = left
	m.Runner.Lanes[right.ID] = right
	m.LaneIDs = []string{left.ID, right.ID}
	m.DiffView = NewDiffView()
	m.DiffView.Open(left, right, fork)
	m.DiffView.SetSize(m.Width, m.Height)
	m.Mode = ViewDiff

	view := m.View()
	rendered := view.Content
	if !strings.Contains(rendered, "L1") || !strings.Contains(rendered, "L2") {
		t.Fatalf("expected diff view rendered with lane labels, got:\n%s", rendered)
	}
}

func TestModel_ViewDiff_HidesEngineLogs(t *testing.T) {
	logBuf, err := steerinspect.NewLogBuffer("", 10)
	if err != nil {
		t.Fatalf("failed to create log buffer: %v", err)
	}
	m := &Model{
		Runner:            &steerinspect.Runner{Lanes: map[string]*steerinspect.Lane{}},
		PromptInitialized: true,
		Width:             100,
		Height:            30,
		ShowLogs:          true,
		LogBuf:            logBuf,
	}
	left, right, fork := buildTwoSiblingLanes(t)
	m.Runner.Lanes[left.ID] = left
	m.Runner.Lanes[right.ID] = right
	m.DiffView = NewDiffView()
	m.DiffView.Open(left, right, fork)
	m.DiffView.SetSize(m.Width, m.Height)
	m.Mode = ViewDiff

	view := m.View()
	rendered := view.Content
	if strings.Contains(rendered, "Engine Logs") {
		t.Fatalf("expected Engine Logs pane to be hidden in ViewDiff, got:\n%s", rendered)
	}
}
