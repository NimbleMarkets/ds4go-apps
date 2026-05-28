package steertui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/steerinspect"
	"github.com/NimbleMarkets/ds4go/ds4api"
)

func TestAlternativesTableView(t *testing.T) {
	// Initialize a mock library and runner
	lib := ds4api.NewMockLibrary()
	ds4api.SetDefaultLibrary(lib)
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close()

	reg := &steerinspect.Registry{
		Dir:     ".",
		Vectors: make(map[string]steerinspect.RegistryVector),
	}
	runner := steerinspect.NewRunner(engine, reg, steerinspect.RunnerOptions{
		TopK: 5,
	})
	defer runner.Close()

	promptTokens, _ := engine.TokenizeText("Hello")
	defer promptTokens.Free()
	_, _ = runner.InitRootLane(promptTokens)

	m := NewModel(runner, "mock-model", "Hello", nil, PromptConfig{})
	activeLane := m.Runner.GetActiveLane()
	if activeLane == nil {
		t.Fatalf("active lane is nil")
	}

	// Add a step with mock alternatives
	step := steerinspect.Step{
		Pos:       1,
		TokenID:   42,
		TokenText: "world",
		Entropy:   0.5,
		Margin:    1.2,
		Alternatives: []steerinspect.Alternative{
			{TokenID: 10, TokenText: "first", Logit: 10.5, Logprob: -0.05},
			{TokenID: 20, TokenText: "second", Logit: 9.0, Logprob: -1.5},
			{TokenID: 30, TokenText: "third", Logit: 8.0, Logprob: -2.5},
		},
	}
	activeLane.Steps = append(activeLane.Steps, step)

	// Case 1: SelectedStepIdx = -1 (next step / live alternatives)
	m.SelectedStepIdx = -1
	t.Logf("SelectedStepIdx = -1, len(steps) = %d", len(activeLane.Steps))
	m.updateAlternativesTable()
	view1 := m.Table.View()
	t.Logf("View 1 (SelectedStepIdx = -1):\n%s\n---", view1)

	// Case 2: SelectedStepIdx = 0
	m.SelectedStepIdx = 0
	m.Table.SetWidth(60)
	m.updateAlternativesTable()
	t.Logf("Table Rows: %d, Height: %d, Width: %d", len(m.Table.Rows()), m.Table.Height(), m.Table.Width())
	view2 := m.Table.View()
	t.Logf("View 2 (SelectedStepIdx = 0):\n%s\n---", view2)
}

func TestAlignment(t *testing.T) {
	label1 := "Total tokens generated: "
	label2 := "Last token:             "
	label3 := "Last step entropy:      "

	t.Logf("label1 len: %d, content: %q", len(label1), label1)
	t.Logf("label2 len: %d, content: %q", len(label2), label2)
	t.Logf("label3 len: %d, content: %q", len(label3), label3)

	str1 := PrimaryStyle.Render("A\n")
	t.Logf("PrimaryStyle.Render(\"A\\n\"): %q", str1)
	str2 := PrimaryStyle.Render("A") + "\n"
	t.Logf("PrimaryStyle.Render(\"A\") + \"\\n\": %q", str2)
}

func TestRenderMetricsView(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	ds4api.SetDefaultLibrary(lib)
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close()

	reg := &steerinspect.Registry{
		Dir:     ".",
		Vectors: make(map[string]steerinspect.RegistryVector),
	}
	runner := steerinspect.NewRunner(engine, reg, steerinspect.RunnerOptions{})
	defer runner.Close()

	promptTokens, _ := engine.TokenizeText("Hello")
	defer promptTokens.Free()
	_, _ = runner.InitRootLane(promptTokens)

	m := NewModel(runner, "mock-model", "Hello", nil, PromptConfig{})
	activeLane := m.Runner.GetActiveLane()

	// Add steps
	activeLane.Steps = append(activeLane.Steps, steerinspect.Step{
		Pos:       1,
		TokenID:   42,
		TokenText: "world",
		Entropy:   0.5,
		Margin:    1.2,
	})

	// Case 1: No step selected (SelectedStepIdx = -1)
	m.SelectedStepIdx = -1
	metrics1 := m.renderMetricsView(10)
	t.Logf("Metrics (No step selected) raw:\n%q\n---", metrics1)

	// Case 2: Step selected (SelectedStepIdx = 0)
	m.SelectedStepIdx = 0
	metrics2 := m.renderMetricsView(10)
	t.Logf("Metrics (Step 0 selected):\n%s\n---", metrics2)
}

func TestStrippedLines(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	ds4api.SetDefaultLibrary(lib)
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close()

	reg := &steerinspect.Registry{
		Dir:     ".",
		Vectors: make(map[string]steerinspect.RegistryVector),
	}
	runner := steerinspect.NewRunner(engine, reg, steerinspect.RunnerOptions{})
	defer runner.Close()

	promptTokens, _ := engine.TokenizeText("Hello")
	defer promptTokens.Free()
	_, _ = runner.InitRootLane(promptTokens)

	m := NewModel(runner, "mock-model", "Hello", nil, PromptConfig{})
	activeLane := m.Runner.GetActiveLane()
	activeLane.Steps = append(activeLane.Steps, steerinspect.Step{
		Pos:       1,
		TokenID:   42,
		TokenText: "world",
		Entropy:   0.5,
		Margin:    1.2,
	})

	m.SelectedStepIdx = -1
	metrics := m.renderMetricsView(10)
	lines := strings.Split(metrics, "\n")
	for i, line := range lines {
		stripped := stripANSI(line)
		t.Logf("Original Line %d: len=%d, content=%q", i, len(stripped), stripped)
	}

	// Now check how it looks if we render without trailing newlines inside the style:
	var sb strings.Builder
	sb.WriteString(PrimaryStyle.Render("Total tokens generated: 1") + "\n")
	sb.WriteString(PrimaryStyle.Render("Last token:             42 (\"world\")") + "\n")
	sb.WriteString(PrimaryStyle.Render("Last step entropy:      0.500") + "\n")

	cleanMetrics := sb.String()
	cleanLines := strings.Split(cleanMetrics, "\n")
	for i, line := range cleanLines {
		stripped := stripANSI(line)
		t.Logf("Clean Line %d: len=%d, content=%q", i, len(stripped), stripped)
	}
}

func TestRenderLogsViewScrollback(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	ds4api.SetDefaultLibrary(lib)
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close()

	reg := &steerinspect.Registry{
		Dir:     ".",
		Vectors: make(map[string]steerinspect.RegistryVector),
	}
	runner := steerinspect.NewRunner(engine, reg, steerinspect.RunnerOptions{})
	defer runner.Close()

	promptTokens, _ := engine.TokenizeText("Hello")
	defer promptTokens.Free()
	_, _ = runner.InitRootLane(promptTokens)

	logBuf, err := steerinspect.NewLogBuffer("", 100)
	if err != nil {
		t.Fatalf("failed to create log buffer: %v", err)
	}
	for i := 0; i < 20; i++ {
		logBuf.WriteLog(ds4api.LogDefault, fmt.Sprintf("line-%02d\n", i))
	}

	m := NewModel(runner, "mock-model", "Hello", logBuf, PromptConfig{})

	// Tail view: should end at line-19, no PAUSED.
	m.LogsScrollOffset = 0
	tail := stripANSI(m.renderLogsView(60, 8))
	if strings.Contains(tail, "PAUSED") {
		t.Errorf("expected tail view to NOT contain PAUSED, got: %q", tail)
	}
	if !strings.Contains(tail, "line-19") {
		t.Errorf("expected tail view to include line-19, got: %q", tail)
	}

	// Scrolled back 3: should end at line-16 (20-3-1=16 inclusive), include PAUSED.
	m.LogsScrollOffset = 3
	paused := stripANSI(m.renderLogsView(60, 8))
	if !strings.Contains(paused, "PAUSED") {
		t.Errorf("expected scrolled view to contain PAUSED, got: %q", paused)
	}
	if !strings.Contains(paused, "line-16") {
		t.Errorf("expected scrolled view to include line-16, got: %q", paused)
	}
	if strings.Contains(paused, "line-19") || strings.Contains(paused, "line-18") || strings.Contains(paused, "line-17") {
		t.Errorf("scrolled view should not show lines past tailEnd, got: %q", paused)
	}
}

func stripANSI(s string) string {
	// Simple ANSI escape sequence remover
	var sb strings.Builder
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEscape = false
			}
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func newTestModel(t *testing.T) *Model {
	t.Helper()
	lib := ds4api.NewMockLibrary()
	ds4api.SetDefaultLibrary(lib)
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	t.Cleanup(func() { engine.Close() })

	reg := &steerinspect.Registry{
		Dir:     ".",
		Vectors: make(map[string]steerinspect.RegistryVector),
	}
	runner := steerinspect.NewRunner(engine, reg, steerinspect.RunnerOptions{})
	t.Cleanup(func() { runner.Close() })

	promptTokens, _ := engine.TokenizeText("Hello")
	t.Cleanup(func() { promptTokens.Free() })
	_, _ = runner.InitRootLane(promptTokens)

	return NewModel(runner, "mock-model", "Hello", nil, PromptConfig{})
}

func TestTabCycleIncludesLogsWhenShown(t *testing.T) {
	m := newTestModel(t)
	m.ShowLogs = true
	m.Focus = FocusTranscript

	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	_, _ = m.Update(tab)
	if m.Focus != FocusAlternatives {
		// Current cycle order: Lanes, Transcript, Alternatives, Metrics, Logs, Prompt.
		// From Transcript, next is Alternatives.
		t.Errorf("expected Focus=FocusAlternatives after tab from Transcript with ShowLogs=true, got %v", m.Focus)
	}
	// Continue tabbing through to verify FocusLogs is hit before FocusPrompt.
	_, _ = m.Update(tab)
	if m.Focus != FocusMetrics {
		t.Errorf("expected Focus=FocusMetrics, got %v", m.Focus)
	}
	_, _ = m.Update(tab)
	if m.Focus != FocusLogs {
		t.Errorf("expected Focus=FocusLogs, got %v", m.Focus)
	}
	_, _ = m.Update(tab)
	if m.Focus != FocusPrompt {
		t.Errorf("expected Focus=FocusPrompt, got %v", m.Focus)
	}
}

func TestTabCycleSkipsLogsWhenHidden(t *testing.T) {
	m := newTestModel(t)
	m.ShowLogs = false
	m.Focus = FocusMetrics

	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	_, _ = m.Update(tab)
	if m.Focus != FocusPrompt {
		t.Errorf("expected Focus=FocusPrompt after tab from Metrics with ShowLogs=false, got %v", m.Focus)
	}
}

func TestToggleOResetsState(t *testing.T) {
	m := newTestModel(t)
	m.ShowLogs = true
	m.Focus = FocusLogs
	m.LogsScrollOffset = 5

	o := tea.KeyPressMsg{Code: 'o', Text: "o"}
	_, _ = m.Update(o)

	if m.ShowLogs {
		t.Errorf("expected ShowLogs=false after 'o', got true")
	}
	if m.LogsScrollOffset != 0 {
		t.Errorf("expected LogsScrollOffset=0 after toggle, got %d", m.LogsScrollOffset)
	}
	if m.Focus != FocusTranscript {
		t.Errorf("expected Focus=FocusTranscript after hiding logs from FocusLogs, got %v", m.Focus)
	}
}

func TestLogsScrollKeysClampAndEnd(t *testing.T) {
	m := newTestModel(t)
	logBuf, err := steerinspect.NewLogBuffer("", 100)
	if err != nil {
		t.Fatalf("failed to create log buffer: %v", err)
	}
	for i := 0; i < 30; i++ {
		logBuf.WriteLog(ds4api.LogDefault, fmt.Sprintf("line-%02d\n", i))
	}
	m.LogBuf = logBuf
	m.ShowLogs = true
	m.Width = 100
	m.Height = 40
	m.Focus = FocusLogs

	up := tea.KeyPressMsg{Code: tea.KeyUp}
	for i := 0; i < 5; i++ {
		_, _ = m.Update(up)
	}
	if m.LogsScrollOffset != 5 {
		t.Errorf("expected LogsScrollOffset=5 after 5 ups, got %d", m.LogsScrollOffset)
	}

	down := tea.KeyPressMsg{Code: tea.KeyDown}
	for i := 0; i < 100; i++ {
		_, _ = m.Update(down)
	}
	if m.LogsScrollOffset != 0 {
		t.Errorf("expected LogsScrollOffset clamped to 0 after many downs, got %d", m.LogsScrollOffset)
	}

	// Many ups: clamp to max(0, len-visible).
	for i := 0; i < 200; i++ {
		_, _ = m.Update(up)
	}
	if m.LogsScrollOffset == 0 {
		t.Errorf("expected LogsScrollOffset > 0 after many ups with 30 lines")
	}
	if m.LogsScrollOffset > 30 {
		t.Errorf("expected LogsScrollOffset <= 30, got %d", m.LogsScrollOffset)
	}

	// 'end' jumps to tail.
	end := tea.KeyPressMsg{Code: tea.KeyEnd}
	_, _ = m.Update(end)
	if m.LogsScrollOffset != 0 {
		t.Errorf("expected LogsScrollOffset=0 after 'end', got %d", m.LogsScrollOffset)
	}
}
