package steertui

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/steerinspect"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ntcharts/v2/sparkline"
)

// StepDoneMsg carries the result of an asynchronous token generation.
type StepDoneMsg struct {
	LaneID string
	Step   steerinspect.Step
	Err    error
}

// PromptConfig contains settings for initializing the prompt session.
type PromptConfig struct {
	System        string
	ThinkMode     ds4api.ThinkMode
	DefaultVector string
	Scales        []float32
	AttnScale     float32
}

// Model is the main Bubble Tea program model.
type Model struct {
	Runner           *steerinspect.Runner
	ModelPath        string
	ModelName        string
	ModelID          int
	PromptText       string
	LogBuf           *steerinspect.LogBuffer
	ShowLogs         bool
	LogsScrollOffset int
	Width            int
	Height           int
	Focus            FocusArea
	SelectedStepIdx  int // -1 means end of generation (real-time)
	SelectedAltIdx   int

	// Lanes list state
	LaneIDs         []string
	SelectedLaneIdx int

	// Overlays
	HelpOpen          bool
	SteeringState     SteeringMenuState
	VectorBrowserOpen bool
	SelectedVectorIdx int

	// Continuous generation state
	Generating bool
	MaxTokens  int
	TokensGen  int
	GenStart   time.Time
	GenLast    time.Time
	GenTokens  int

	// Status messages
	StatusMsg  string
	StatusTime time.Time

	// Diff view state
	Mode           ViewMode
	DiffView       *DiffView // nil until first diff is opened
	DiffPickerOpen bool
	DiffPickerIdx  int

	// Prompt input state
	PromptInitialized bool
	PromptInput       textarea.Model
	PromptConfig      PromptConfig

	// Bubbles components
	Table table.Model
}

// NewModel creates an initialized UI Model.
func NewModel(runner *steerinspect.Runner, modelPath, prompt string, logBuf *steerinspect.LogBuffer, promptCfg PromptConfig) *Model {
	columns := []table.Column{
		{Title: "Rank", Width: 4},
		{Title: "Token text", Width: 10},
		{Title: "Prob", Width: 8},
		{Title: "Distribution", Width: 12},
		{Title: "Logit", Width: 7},
		{Title: "Logprob", Width: 10},
	}
	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(7),
		table.WithWidth(50),
	)

	ti := textarea.New()
	ti.Placeholder = "Enter prompt..."
	ti.ShowLineNumbers = false
	ti.SetHeight(3)

	var focus FocusArea
	if prompt == "" {
		ti.Focus()
		focus = FocusPrompt
	} else {
		ti.SetValue(prompt)
		ti.Blur()
		focus = FocusTranscript
	}

	return &Model{
		Runner:            runner,
		ModelPath:         modelPath,
		ModelName:         runner.Engine.ModelName(),
		ModelID:           runner.Engine.ModelID(),
		PromptText:        prompt,
		LogBuf:            logBuf,
		Focus:             focus,
		SelectedStepIdx:   -1,
		MaxTokens:         100,
		PromptInitialized: prompt != "",
		PromptConfig:      promptCfg,
		Table:             t,
		PromptInput:       ti,
	}
}

func (m *Model) initRootLaneFromPrompt(prompt string) error {
	m.PromptText = prompt
	engine := m.Runner.Engine

	promptTokens, err := engine.EncodeChatPrompt(m.PromptConfig.System, prompt, m.PromptConfig.ThinkMode)
	if err != nil {
		return fmt.Errorf("failed to encode chat prompt: %w", err)
	}
	defer promptTokens.Free()

	rootID, err := m.Runner.InitRootLane(promptTokens)
	if err != nil {
		return fmt.Errorf("failed to initialize root lane: %w", err)
	}

	defaultVector := m.PromptConfig.DefaultVector
	scales := m.PromptConfig.Scales
	attnScale := m.PromptConfig.AttnScale

	if defaultVector == "" && len(m.Runner.Registry.Vectors) > 0 {
		for name := range m.Runner.Registry.Vectors {
			defaultVector = name
			break
		}
	}

	dynamicSteeringAvailable := true
	if len(scales) > 0 && defaultVector != "" {
		firstScale := scales[0]
		if firstScale != 0 {
			cfgSteer := &steerinspect.SteeringConfig{
				Vector:    defaultVector,
				Mode:      steerinspect.SteeringAdditive,
				FFNScale:  firstScale,
				AttnScale: attnScale,
				Threshold: 0,
				Scope:     steerinspect.SteeringScopeUntilRevert,
			}
			if err := m.Runner.ApplyManualSteering(rootID, cfgSteer); err != nil {
				if errors.Is(err, ds4api.ErrSteeringNotSupported) {
					dynamicSteeringAvailable = false
					m.setStatus("Dynamic steering not available in loaded libds4; continuing without applied scales.")
					if m.LogBuf != nil {
						m.LogBuf.WriteLog(ds4api.LogWarning, "Dynamic steering not available; --scale ignored.\n")
					}
				} else {
					return fmt.Errorf("failed to apply steering scale %f to root lane: %w", firstScale, err)
				}
			}
		}
	}

	activeLane := m.Runner.GetActiveLane()
	if activeLane == nil {
		return fmt.Errorf("no active lane after initialization")
	}
	initialPos := activeLane.InitialPos
	for i := 1; i < len(scales); i++ {
		scale := scales[i]
		branchID, err := m.Runner.BranchLane(rootID, initialPos)
		if err != nil {
			return fmt.Errorf("failed to branch lane for scale %f: %w", scale, err)
		}
		if scale != 0 && defaultVector != "" && dynamicSteeringAvailable {
			cfgSteer := &steerinspect.SteeringConfig{
				Vector:    defaultVector,
				Mode:      steerinspect.SteeringAdditive,
				FFNScale:  scale,
				AttnScale: attnScale,
				Threshold: 0,
				Scope:     steerinspect.SteeringScopeUntilRevert,
			}
			if err := m.Runner.ApplyManualSteering(branchID, cfgSteer); err != nil {
				if errors.Is(err, ds4api.ErrSteeringNotSupported) {
					dynamicSteeringAvailable = false
				} else {
					return fmt.Errorf("failed to apply steering scale %f to branched lane %s: %w", scale, branchID, err)
				}
			}
		}
	}

	m.updateLaneList()
	m.PromptInitialized = true
	return nil
}

// Init initializes the Bubble Tea program.
func (m *Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	cmds = append(cmds, textarea.Blink)

	if m.PromptInitialized && m.PromptText != "" {
		if err := m.initRootLaneFromPrompt(m.PromptText); err != nil {
			m.setStatus(fmt.Sprintf("Init Error: %v", err))
			if m.LogBuf != nil {
				m.LogBuf.WriteLog(ds4api.LogError, fmt.Sprintf("Init Error: %v\n", err))
			}
			return tea.Quit
		}
	}
	m.updateLaneList()
	return tea.Batch(cmds...)
}

func (m *Model) updateLaneList() {
	lanes := m.Runner.GetLanes()
	ids := make([]string, 0, len(lanes))
	for id := range lanes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	m.LaneIDs = ids

	// Keep selection in bounds
	if m.SelectedLaneIdx >= len(m.LaneIDs) {
		m.SelectedLaneIdx = len(m.LaneIDs) - 1
	}
	if m.SelectedLaneIdx < 0 && len(m.LaneIDs) > 0 {
		m.SelectedLaneIdx = 0
	}
}

func (m *Model) getSortedVectorNames() []string {
	var names []string
	if m.Runner.Registry != nil {
		for name := range m.Runner.Registry.Vectors {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (m *Model) getSteeringVectorNames() []string {
	names := []string{"[None / Disable]"}
	sorted := m.getSortedVectorNames()
	return append(names, sorted...)
}

func (m *Model) tokenRate() float64 {
	if m.GenStart.IsZero() || m.GenTokens == 0 {
		return 0
	}
	end := time.Now()
	if !m.Generating && !m.GenLast.IsZero() {
		end = m.GenLast
	}
	secs := end.Sub(m.GenStart).Seconds()
	if secs <= 0 {
		return 0
	}
	return float64(m.GenTokens) / secs
}

// setStatus sets a temporary status bar message.
func (m *Model) setStatus(msg string) {
	m.StatusMsg = msg
	m.StatusTime = time.Now()
}

// generateTokenCmd wraps the synchronous runner GenerateOne into a Bubble Tea command.
func (m *Model) generateTokenCmd(laneID string, forcedToken *int) tea.Cmd {
	return func() tea.Msg {
		step, err := m.Runner.GenerateOne(laneID, forcedToken)
		return StepDoneMsg{
			LaneID: laneID,
			Step:   step,
			Err:    err,
		}
	}
}

// Update handles message loops and state transitions.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height

		promptHeight := msg.Height * 15 / 100
		if promptHeight < 5 {
			promptHeight = 5
		}
		innerHeight := promptHeight - 2
		m.PromptInput.SetHeight(innerHeight)
		m.PromptInput.SetWidth(msg.Width - 4)
		return m, nil

	case StepDoneMsg:
		if msg.Err != nil {
			m.Generating = false
			m.setStatus(fmt.Sprintf("Generation Error: %v", msg.Err))
			return m, nil
		}

		m.TokensGen++
		m.GenTokens++
		m.GenLast = time.Now()

		// Check if we should keep generating.
		activeLane := m.Runner.GetActiveLane()
		if m.Generating && activeLane != nil && len(activeLane.Steps) > 0 {
			lastStep := activeLane.Steps[len(activeLane.Steps)-1]
			// Stop on EOS (1 is EOS in mock library, or get from engine).
			eos := 1
			if m.Runner.Engine != nil {
				eos = m.Runner.Engine.TokenEOS()
			}
			if lastStep.TokenID == eos {
				m.Generating = false
				m.setStatus(fmt.Sprintf("Generation complete. Added %d tokens.", m.TokensGen))
			} else {
				return m, m.generateTokenCmd(m.Runner.ActiveLaneID, nil)
			}
		}
		return m, nil
	}

	// Intercept keys if prompt input is focused
	if m.Focus == FocusPrompt {
		switch msg := msg.(type) {
		case tea.KeyPressMsg:
			key := msg.String()
			switch key {
			case "ctrl+c":
				m.Runner.Close()
				return m, tea.Quit
			case "esc":
				m.PromptInput.Blur()
				m.Focus = FocusTranscript
				return m, nil
			case "enter":
				trimmed := strings.TrimSpace(m.PromptInput.Value())
				if trimmed == "" {
					m.setStatus("Prompt cannot be empty")
				} else {
					m.PromptInput.Blur()
					m.Focus = FocusTranscript
					m.Runner.Reset()
					m.SelectedStepIdx = -1
					m.SelectedAltIdx = 0
					m.SelectedLaneIdx = 0
					m.LaneIDs = nil
					m.PromptInitialized = false
					if err := m.initRootLaneFromPrompt(trimmed); err != nil {
						m.setStatus(fmt.Sprintf("Failed to initialize session: %v", err))
					}
				}
				return m, nil
			default:
				var cmd tea.Cmd
				m.PromptInput, cmd = m.PromptInput.Update(msg)
				return m, cmd
			}
		}
	}

	// Handle interactive overlay controls.
	if m.HelpOpen {
		switch msg := msg.(type) {
		case tea.KeyPressMsg:
			switch msg.String() {
			case "esc", "?", "q":
				m.HelpOpen = false
			}
		}
		return m, nil
	}

	if m.VectorBrowserOpen {
		switch msg := msg.(type) {
		case tea.KeyPressMsg:
			switch msg.String() {
			case "esc", "v", "q":
				m.VectorBrowserOpen = false
			case "up", "k":
				names := m.getSortedVectorNames()
				if len(names) > 0 {
					m.SelectedVectorIdx = (m.SelectedVectorIdx - 1 + len(names)) % len(names)
				}
			case "down", "j":
				names := m.getSortedVectorNames()
				if len(names) > 0 {
					m.SelectedVectorIdx = (m.SelectedVectorIdx + 1) % len(names)
				}
			case "enter":
				names := m.getSortedVectorNames()
				if len(names) > 0 && m.SelectedVectorIdx >= 0 && m.SelectedVectorIdx < len(names) {
					m.VectorBrowserOpen = false
					m.SteeringState.Active = true
					m.SteeringState.FieldIndex = 0
					m.SteeringState.FFNScaleStr = "1.0"
					m.SteeringState.AttnScaleStr = "0.0"
					m.SteeringState.ThresholdStr = "0.0"
					m.SteeringState.VectorSelectionIndex = m.SelectedVectorIdx + 1
				}
			}
		}
		return m, nil
	}

	if m.DiffPickerOpen {
		switch msg := msg.(type) {
		case tea.KeyPressMsg:
			pairs := steerinspect.EligiblePairs(m.Runner.GetLanes())
			switch msg.String() {
			case "esc":
				m.DiffPickerOpen = false
			case "j", "down":
				if m.DiffPickerIdx < len(pairs)-1 {
					m.DiffPickerIdx++
				}
			case "k", "up":
				if m.DiffPickerIdx > 0 {
					m.DiffPickerIdx--
				}
			case "enter":
				if m.DiffPickerIdx < len(pairs) {
					p := pairs[m.DiffPickerIdx]
					left := m.Runner.Lanes[p.LeftID]
					right := m.Runner.Lanes[p.RightID]
					if m.DiffView == nil {
						m.DiffView = NewDiffView()
					}
					m.DiffView.Open(left, right, p.ForkStep)
					m.DiffView.SetSize(m.Width, m.Height)
					m.Mode = ViewDiff
					m.DiffPickerOpen = false
				}
			}
		}
		return m, nil
	}

	if m.Mode == ViewDiff && m.DiffView != nil {
		switch msg := msg.(type) {
		case tea.KeyPressMsg:
			switch msg.String() {
			case "esc":
				m.Mode = ViewNormal
			case "C":
				m.Mode = ViewNormal
				m.openDiffPicker()
			case "enter":
				m.DiffView.ToggleCollapsed()
			case "s":
				m.DiffView.ToggleSteeringGlyph()
			default:
				cmd := m.DiffView.Update(msg)
				return m, cmd
			}
		}
		return m, nil
	}

	if m.SteeringState.Active {
		return m.updateSteeringMenu(msg)
	}

	// General navigation and execution keybindings.
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "ctrl+c", "q":
			m.Runner.Close()
			return m, tea.Quit

		case "esc":
			if m.Generating {
				m.Generating = false
				m.setStatus("Generation paused.")
			}

		case "tab":
			// Cycle pane focus. Skip FocusLogs when logs row is hidden.
			for {
				m.Focus = FocusArea((int(m.Focus) + 1) % 6)
				if m.Focus == FocusLogs && !m.ShowLogs {
					continue
				}
				break
			}
			if m.Focus == FocusPrompt {
				m.PromptInput.Focus()
			} else {
				m.PromptInput.Blur()
			}

		case "e":
			m.Focus = FocusPrompt
			m.PromptInput.Focus()

		case "n":
			m.PromptInput.SetValue("")
			m.Focus = FocusPrompt
			m.PromptInput.Focus()

		case "v":
			if m.Runner.Registry != nil && len(m.Runner.Registry.Vectors) > 0 {
				m.VectorBrowserOpen = true
				m.SelectedVectorIdx = 0
			} else {
				m.setStatus("No registered steering vectors available.")
			}

		case "?":
			m.HelpOpen = true

		case "ctrl+s":
			m.SteeringState.Active = true
			m.SteeringState.FieldIndex = 0
			m.SteeringState.FFNScaleStr = "1.0"
			m.SteeringState.AttnScaleStr = "0.0"
			m.SteeringState.ThresholdStr = "0.0"

		case "space":
			if !m.Generating {
				m.setStatus("Generating single token...")
				m.GenStart = time.Now()
				m.GenLast = time.Time{}
				m.GenTokens = 0
				return m, m.generateTokenCmd(m.Runner.ActiveLaneID, nil)
			}

		case "r":
			// Rewind active lane.
			activeLane := m.Runner.GetActiveLane()
			if activeLane != nil && len(activeLane.Steps) > 0 {
				targetIdx := m.SelectedStepIdx
				if targetIdx == -1 {
					targetIdx = len(activeLane.Steps) - 1
				}
				step := activeLane.Steps[targetIdx]
				err := activeLane.Rewind(step.Pos)
				if err != nil {
					m.setStatus(fmt.Sprintf("Rewind error: %v", err))
				} else {
					m.SelectedStepIdx = -1
					m.setStatus(fmt.Sprintf("Rewound lane %s to pos %d", activeLane.ID, step.Pos))
				}
			}

		case "b":
			// Branch lane.
			activeLane := m.Runner.GetActiveLane()
			if activeLane != nil && len(activeLane.Steps) > 0 {
				targetIdx := m.SelectedStepIdx
				if targetIdx == -1 {
					targetIdx = len(activeLane.Steps) - 1
				}
				step := activeLane.Steps[targetIdx]

				// Get selected alternative token.
				alts := step.Alternatives
				if m.SelectedStepIdx == -1 {
					// Fetch live alternatives.
					var err error
					alts, err = m.getLiveAlternatives(activeLane)
					if err != nil {
						m.setStatus(fmt.Sprintf("Branch error: %v", err))
						return m, nil
					}
				}

				if m.SelectedAltIdx >= 0 && m.SelectedAltIdx < len(alts) {
					forced := alts[m.SelectedAltIdx].TokenID
					m.setStatus(fmt.Sprintf("Branching lane at pos %d using token %d...", step.Pos, forced))

					newLaneID, err := m.Runner.BranchLane(activeLane.ID, step.Pos+1)
					if err != nil {
						m.setStatus(fmt.Sprintf("Branch error: %v", err))
					} else {
						m.updateLaneList()
						m.SelectedStepIdx = -1
						// Auto-generate the forced token in the new branch.
						return m, m.generateTokenCmd(newLaneID, &forced)
					}
				} else {
					m.setStatus("Branch error: please select a token alternative first.")
				}
			}

		case "c":
			// Re-enter saved diff if one exists, otherwise open picker.
			// (ViewDiff exit is handled in the earlier ViewDiff overlay block.)
			if m.DiffView != nil && m.DiffView.HasPair() {
				m.Mode = ViewDiff
				break
			}
			m.openDiffPicker()
		case "C":
			m.openDiffPicker()

		case "o":
			m.ShowLogs = !m.ShowLogs
			m.LogsScrollOffset = 0
			if m.ShowLogs {
				m.setStatus("Showing Engine Logs.")
			} else {
				if m.Focus == FocusLogs {
					m.Focus = FocusTranscript
				}
				m.setStatus("Hiding Engine Logs.")
			}

		case "[":
			// Move SelectedStepIdx backward.
			activeLane := m.Runner.GetActiveLane()
			if activeLane != nil && len(activeLane.Steps) > 0 {
				if m.SelectedStepIdx == -1 {
					m.SelectedStepIdx = len(activeLane.Steps) - 1
				} else if m.SelectedStepIdx > 0 {
					m.SelectedStepIdx--
				}
			}

		case "]":
			// Move SelectedStepIdx forward.
			activeLane := m.Runner.GetActiveLane()
			if activeLane != nil && len(activeLane.Steps) > 0 {
				if m.SelectedStepIdx != -1 {
					if m.SelectedStepIdx < len(activeLane.Steps)-1 {
						m.SelectedStepIdx++
					} else {
						m.SelectedStepIdx = -1 // Go back to real-time end
					}
				}
			}

		case "up", "k":
			if m.Focus == FocusLogs {
				m.LogsScrollOffset++
				m.clampLogsOffset()
			} else if m.Focus == FocusAlternatives {
				m.Table, _ = m.Table.Update(msg)
				m.SelectedAltIdx = m.Table.Cursor()
			} else {
				m.handleNavigationUp()
			}

		case "down", "j":
			if m.Focus == FocusLogs {
				m.LogsScrollOffset--
				m.clampLogsOffset()
			} else if m.Focus == FocusAlternatives {
				m.Table, _ = m.Table.Update(msg)
				m.SelectedAltIdx = m.Table.Cursor()
			} else {
				m.handleNavigationDown()
			}

		case "pgup":
			if m.Focus == FocusLogs {
				m.LogsScrollOffset += m.logsVisibleLines()
				m.clampLogsOffset()
			}

		case "pgdown":
			if m.Focus == FocusLogs {
				m.LogsScrollOffset -= m.logsVisibleLines()
				m.clampLogsOffset()
			}

		case "home", "g":
			if m.Focus == FocusLogs {
				// Jump to top: max offset.
				if m.LogBuf != nil {
					lines := m.LogBuf.GetLines()
					m.LogsScrollOffset = len(lines)
					m.clampLogsOffset()
				}
			}

		case "end", "G":
			if m.Focus == FocusLogs {
				m.LogsScrollOffset = 0
			}

		case "shift+up":
			if m.Focus == FocusTranscript {
				m.handleNavigationUp5()
			}

		case "shift+down":
			if m.Focus == FocusTranscript {
				m.handleNavigationDown5()
			}

		case "enter":
			if m.Focus == FocusLanes || m.Focus == FocusAlternatives {
				m.handleSelectionAction()
			} else {
				if m.Generating {
					m.Generating = false
					m.setStatus("Generation paused.")
				} else {
					m.Generating = true
					m.TokensGen = 0
					m.GenStart = time.Now()
					m.GenLast = time.Time{}
					m.GenTokens = 0
					m.setStatus("Generating continuously...")
					return m, m.generateTokenCmd(m.Runner.ActiveLaneID, nil)
				}
			}
		}
	}

	if _, ok := msg.(tea.KeyPressMsg); !ok {
		var cmd tea.Cmd
		m.PromptInput, cmd = m.PromptInput.Update(msg)
		if cmd != nil {
			return m, cmd
		}
	}

	return m, nil
}

func (m *Model) handleNavigationUp() {
	switch m.Focus {
	case FocusLanes:
		if m.SelectedLaneIdx > 0 {
			m.SelectedLaneIdx--
		}
	case FocusTranscript:
		activeLane := m.Runner.GetActiveLane()
		if activeLane != nil && len(activeLane.Steps) > 0 {
			if m.SelectedStepIdx == -1 {
				m.SelectedStepIdx = len(activeLane.Steps) - 1
			} else if m.SelectedStepIdx > 0 {
				m.SelectedStepIdx--
			}
		}
	}
}

func (m *Model) handleNavigationDown() {
	switch m.Focus {
	case FocusLanes:
		if m.SelectedLaneIdx < len(m.LaneIDs)-1 {
			m.SelectedLaneIdx++
		}
	case FocusTranscript:
		activeLane := m.Runner.GetActiveLane()
		if activeLane != nil && len(activeLane.Steps) > 0 {
			if m.SelectedStepIdx != -1 {
				if m.SelectedStepIdx < len(activeLane.Steps)-1 {
					m.SelectedStepIdx++
				} else {
					m.SelectedStepIdx = -1
				}
			}
		}
	}
}

func (m *Model) handleNavigationUp5() {
	activeLane := m.Runner.GetActiveLane()
	if activeLane == nil || len(activeLane.Steps) == 0 {
		return
	}
	if m.SelectedStepIdx == -1 {
		idx := len(activeLane.Steps) - 5
		if idx < 0 {
			idx = 0
		}
		m.SelectedStepIdx = idx
	} else {
		idx := m.SelectedStepIdx - 5
		if idx < 0 {
			idx = 0
		}
		m.SelectedStepIdx = idx
	}
}

func (m *Model) handleNavigationDown5() {
	activeLane := m.Runner.GetActiveLane()
	if activeLane == nil || len(activeLane.Steps) == 0 {
		return
	}
	if m.SelectedStepIdx != -1 {
		idx := m.SelectedStepIdx + 5
		if idx >= len(activeLane.Steps) {
			m.SelectedStepIdx = -1
		} else {
			m.SelectedStepIdx = idx
		}
	}
}

func (m *Model) handleSelectionAction() {
	switch m.Focus {
	case FocusLanes:
		// Switch active lane.
		if m.SelectedLaneIdx >= 0 && m.SelectedLaneIdx < len(m.LaneIDs) {
			m.Runner.ActiveLaneID = m.LaneIDs[m.SelectedLaneIdx]
			m.SelectedStepIdx = -1
			m.SelectedAltIdx = 0
			m.setStatus(fmt.Sprintf("Switched active lane to %s", m.Runner.ActiveLaneID))
		}
	case FocusAlternatives:
		// Force selected alternative token in the active lane immediately.
		activeLane := m.Runner.GetActiveLane()
		if activeLane != nil {
			var alts []steerinspect.Alternative
			if m.SelectedStepIdx == -1 {
				alts, _ = m.getLiveAlternatives(activeLane)
			} else {
				alts = activeLane.Steps[m.SelectedStepIdx].Alternatives
			}

			if m.SelectedAltIdx >= 0 && m.SelectedAltIdx < len(alts) {
				forced := alts[m.SelectedAltIdx].TokenID
				m.setStatus(fmt.Sprintf("Forced token choice %d", forced))
				// If we are at a past step, rewind first.
				if m.SelectedStepIdx != -1 && m.SelectedStepIdx < len(activeLane.Steps)-1 {
					_ = activeLane.Rewind(activeLane.Steps[m.SelectedStepIdx].Pos)
					m.SelectedStepIdx = -1
				}
				// Trigger generation with forced token.
				m.generateTokenCmd(activeLane.ID, &forced)()
			}
		}
	}
}

func (m *Model) getLiveAlternatives(lane *steerinspect.Lane) ([]steerinspect.Alternative, error) {
	scores, err := lane.Session.TopLogprobs(m.Runner.TopK)
	if err != nil {
		return nil, err
	}
	alts := make([]steerinspect.Alternative, len(scores))
	for i, s := range scores {
		txt, _ := m.Runner.Engine.TokenText(s.ID)
		alts[i] = steerinspect.Alternative{
			TokenID:   s.ID,
			TokenText: txt,
			Logit:     s.Logit,
			Logprob:   s.Logprob,
		}
	}
	return alts, nil
}

func (m *Model) updateSteeringMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "esc":
			m.SteeringState.Active = false

		case "up", "k":
			if m.SteeringState.FieldIndex > 0 {
				m.SteeringState.FieldIndex--
			}

		case "down", "j":
			if m.SteeringState.FieldIndex < 7 {
				m.SteeringState.FieldIndex++
			}

		case "left", "h":
			m.cycleMenuValue(-1)

		case "right", "l":
			m.cycleMenuValue(1)

		case "enter":
			if m.SteeringState.FieldIndex == 6 {
				// Apply steering parameters
				m.applySteeringMenuSettings()
				m.SteeringState.Active = false
			} else if m.SteeringState.FieldIndex == 7 {
				// Cancel
				m.SteeringState.Active = false
			} else {
				// Advance to next field on enter.
				m.SteeringState.FieldIndex = (m.SteeringState.FieldIndex + 1) % 8
			}

		// Allow typing digits and floating point dots for scales.
		default:
			if len(key) == 1 && ((key[0] >= '0' && key[0] <= '9') || key[0] == '.' || key[0] == '-') {
				switch m.SteeringState.FieldIndex {
				case 1:
					m.SteeringState.FFNScaleStr += key
				case 2:
					m.SteeringState.AttnScaleStr += key
				case 3:
					m.SteeringState.ThresholdStr += key
				}
			} else if key == "backspace" {
				switch m.SteeringState.FieldIndex {
				case 1:
					if len(m.SteeringState.FFNScaleStr) > 0 {
						m.SteeringState.FFNScaleStr = m.SteeringState.FFNScaleStr[:len(m.SteeringState.FFNScaleStr)-1]
					}
				case 2:
					if len(m.SteeringState.AttnScaleStr) > 0 {
						m.SteeringState.AttnScaleStr = m.SteeringState.AttnScaleStr[:len(m.SteeringState.AttnScaleStr)-1]
					}
				case 3:
					if len(m.SteeringState.ThresholdStr) > 0 {
						m.SteeringState.ThresholdStr = m.SteeringState.ThresholdStr[:len(m.SteeringState.ThresholdStr)-1]
					}
				}
			}
		}
	}
	return m, nil
}

func (m *Model) cycleMenuValue(dir int) {
	vectorNames := m.getSteeringVectorNames()

	switch m.SteeringState.FieldIndex {
	case 0:
		// Vector name
		m.SteeringState.VectorSelectionIndex = (m.SteeringState.VectorSelectionIndex + dir + len(vectorNames)) % len(vectorNames)

	case 4:
		// Mode
		m.SteeringState.ModeIndex = (m.SteeringState.ModeIndex + dir + len(modes)) % len(modes)

	case 5:
		// Scope
		m.SteeringState.ScopeIndex = (m.SteeringState.ScopeIndex + dir + len(scopes)) % len(scopes)
	}
}

func (m *Model) applySteeringMenuSettings() {
	activeLaneID := m.Runner.ActiveLaneID
	vectorNames := m.getSteeringVectorNames()

	if m.SteeringState.VectorSelectionIndex == 0 {
		// Disable steering
		err := m.Runner.ApplyManualSteering(activeLaneID, nil)
		if err != nil {
			m.setStatus(fmt.Sprintf("Failed to clear steering: %v", err))
		} else {
			m.setStatus("Manual steering cleared.")
		}
		return
	}

	selectedVector := vectorNames[m.SteeringState.VectorSelectionIndex]
	ffn, _ := strconv.ParseFloat(m.SteeringState.FFNScaleStr, 32)
	attn, _ := strconv.ParseFloat(m.SteeringState.AttnScaleStr, 32)
	thresh, _ := strconv.ParseFloat(m.SteeringState.ThresholdStr, 32)

	cfg := &steerinspect.SteeringConfig{
		Vector:    selectedVector,
		FFNScale:  float32(ffn),
		AttnScale: float32(attn),
		Threshold: float32(thresh),
		Mode:      modes[m.SteeringState.ModeIndex],
		Scope:     scopes[m.SteeringState.ScopeIndex],
	}

	err := m.Runner.ApplyManualSteering(activeLaneID, cfg)
	if err != nil {
		m.setStatus(fmt.Sprintf("Failed to apply steering: %v", err))
	} else {
		m.setStatus(fmt.Sprintf("Steering %q applied successfully.", selectedVector))
	}
}

// View constructs the text presentation based on active focus and overlays.
func (m *Model) View() tea.View {
	if m.Width == 0 || m.Height == 0 {
		return tea.NewView("Loading steering inspector...")
	}

	if m.Mode == ViewDiff && m.DiffView != nil {
		m.DiffView.SetSize(m.Width, m.Height-1)
		statusView := m.renderStatusView()
		body := m.DiffView.View()
		full := body + "\n" + statusView
		return tea.NewView(full)
	}

	if m.DiffPickerOpen {
		pairs := steerinspect.EligiblePairs(m.Runner.GetLanes())
		overlay := RenderDiffPicker(m.Width, m.Height, pairs, m.DiffPickerIdx)
		v := tea.NewView(overlay)
		return v
	}

	var promptTitle string
	if m.Focus == FocusPrompt {
		promptTitle = "Prompt — esc exit · enter submit"
	} else {
		promptTitle = "Prompt — e edit · n new"
	}

	promptHeight := m.Height * 15 / 100
	if promptHeight < 5 {
		promptHeight = 5
	}
	innerHeight := promptHeight - 2
	promptBox := RenderPromptBox(m.Width, m.PromptInput.View(), promptTitle, m.Focus == FocusPrompt, innerHeight)

	statusView := m.renderStatusView()
	wTranscript, hTranscript, wAlts, hAlts, wLanes, hLanes, wMetrics, hMetrics, wLogs, hLogs := GetLayoutDimensions(m.Width, m.Height, statusView, m.ShowLogs)

	// Inner content heights (subtracting 2 for borders)
	hTranscriptInner := hTranscript - 2
	if hTranscriptInner < 1 {
		hTranscriptInner = 1
	}
	hAltsInner := hAlts - 2
	if hAltsInner < 1 {
		hAltsInner = 1
	}
	hLanesInner := hLanes - 2
	if hLanesInner < 1 {
		hLanesInner = 1
	}
	hMetricsInner := hMetrics - 2
	if hMetricsInner < 1 {
		hMetricsInner = 1
	}

	// Inner content widths
	wTranscriptInner := wTranscript - 2
	if wTranscriptInner < 1 {
		wTranscriptInner = 1
	}
	wAltsInner := wAlts - 2
	if wAltsInner < 1 {
		wAltsInner = 1
	}
	wLanesInner := wLanes - 2
	if wLanesInner < 1 {
		wLanesInner = 1
	}
	wMetricsInner := wMetrics - 2
	if wMetricsInner < 1 {
		wMetricsInner = 1
	}

	// Render view segments using computed inner dimensions.
	lanesView := m.renderLanesView(wLanesInner, hLanesInner)
	transcriptView := m.renderTranscriptView(wTranscriptInner, hTranscriptInner)
	alternativesView := m.renderAlternativesView(wAltsInner, hAltsInner)

	metricsView := m.renderMetricsView(wMetricsInner, hMetricsInner)

	var logsView string
	if m.ShowLogs {
		wLogsInner := wLogs - 2
		if wLogsInner < 1 {
			wLogsInner = 1
		}
		hLogsInner := hLogs - 2
		if hLogsInner < 1 {
			hLogsInner = 1
		}
		logsView = m.renderLogsView(wLogsInner, hLogsInner)
	}

	content := RenderLayout(m.Width, m.Height, m.Focus, lanesView, transcriptView, alternativesView, metricsView, logsView, promptBox, statusView, m.ShowLogs)

	// Layer overlays on top of the layout.
	if m.HelpOpen {
		content = RenderHelpOverlay(m.Width, m.Height)
	} else if m.VectorBrowserOpen {
		content = RenderVectorBrowser(m.Width, m.Height, m.Runner.Registry, m.SelectedVectorIdx)
	} else if m.SteeringState.Active {
		content = RenderSteeringMenu(m.Width, m.Height, &m.SteeringState, m.Runner.Registry)
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m *Model) renderLanesView(width, height int) string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(" Lanes ") + "\n")

	linesWritten := 1
	for i, id := range m.LaneIDs {
		if linesWritten >= height {
			break
		}
		lane := m.Runner.Lanes[id]
		marker := "  "
		if id == m.Runner.ActiveLaneID {
			marker = "▸ "
		}

		style := PrimaryStyle
		if i == m.SelectedLaneIdx && m.Focus == FocusLanes {
			style = SelectedStyle
		}

		info := fmt.Sprintf("tokens=%d", len(lane.Steps))
		if lane.ParentID != "" {
			info += fmt.Sprintf(" (fork %s@%d)", lane.ParentID, lane.ParentPos)
		}

		// Check if active steering config is applied.
		steer := m.Runner.GetCurrentSteering(id)
		if steer != nil {
			info += fmt.Sprintf(" [steer: %s]", steer.Vector)
		}

		lineText := marker + fmt.Sprintf("%s: %s", id, info)
		if len(lineText) > width {
			lineText = lineText[:width]
		}
		sb.WriteString(style.Render(lineText) + "\n")
		linesWritten++
	}

	linesList := strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n")
	if len(linesList) > height {
		linesList = linesList[:height]
	}
	return strings.Join(linesList, "\n")
}

func (m *Model) renderTranscriptView(width, height int) string {
	activeLane := m.Runner.GetActiveLane()

	innerWidth := width
	innerHeight := height
	if innerWidth < 5 {
		innerWidth = 5
	}

	headerLines := 2 // title + blank line
	availHeight := innerHeight - headerLines
	if availHeight < 1 {
		availHeight = 1
	}

	if activeLane == nil || len(activeLane.Steps) == 0 {
		content := MutedStyle.Render("No generation steps yet. Press <Space> to decode one step, or <ENTER> to run continuously.")
		// Wrap content and pad/clamp to availHeight
		lines := strings.Split(lipgloss.NewStyle().Width(innerWidth).Render(content), "\n")
		for len(lines) < availHeight {
			lines = append(lines, "")
		}
		if len(lines) > availHeight {
			lines = lines[:availHeight]
		}

		var viewBuilder strings.Builder
		viewBuilder.WriteString(TitleStyle.Render(" Transcript ") + "\n\n")
		viewBuilder.WriteString(strings.Join(lines, "\n"))
		return viewBuilder.String()
	}

	var sb strings.Builder
	var lastSteer *steerinspect.SteeringConfig
	for idx, s := range activeLane.Steps {
		// Highlight steering transitions inline.
		if s.SteerApplied != nil && (lastSteer == nil || lastSteer.Vector != s.SteerApplied.Vector || lastSteer.FFNScale != s.SteerApplied.FFNScale) {
			sb.WriteString(SteerHighlight.Render(fmt.Sprintf(" [Steer: %s ffn=%.1f] ", s.SteerApplied.Vector, s.SteerApplied.FFNScale)))
			lastSteer = s.SteerApplied
		} else if s.SteerApplied == nil && lastSteer != nil {
			sb.WriteString(SteerHighlight.Render(" [Steer: Off] "))
			lastSteer = nil
		}

		txt := s.TokenText
		if idx == m.SelectedStepIdx {
			sb.WriteString(SelectedStyle.Render(txt) + "\u200b")
		} else {
			sb.WriteString(txt)
		}
	}

	// Wrap text to innerWidth
	wrapped := lipgloss.NewStyle().Width(innerWidth).Render(sb.String())
	lines := strings.Split(wrapped, "\n")

	// Find cursor line
	cursorLine := -1
	if m.SelectedStepIdx != -1 {
		for i, line := range lines {
			if strings.Contains(line, "\u200b") {
				cursorLine = i
				break
			}
		}
	}

	// Strip the zero-width space marker
	for i, line := range lines {
		lines[i] = strings.ReplaceAll(line, "\u200b", "")
	}

	// Scroll viewport
	totalLines := len(lines)
	startLine := 0
	if totalLines > availHeight {
		if cursorLine == -1 {
			cursorLine = totalLines - 1
		}
		startLine = cursorLine - availHeight/2
		if startLine < 0 {
			startLine = 0
		}
		if startLine+availHeight > totalLines {
			startLine = totalLines - availHeight
		}
		lines = lines[startLine : startLine+availHeight]
	} else {
		// Pad to exactly availHeight
		for len(lines) < availHeight {
			lines = append(lines, "")
		}
	}

	var viewBuilder strings.Builder
	viewBuilder.WriteString(TitleStyle.Render(" Transcript ") + "\n\n")
	viewBuilder.WriteString(strings.Join(lines, "\n"))
	linesList := strings.Split(strings.TrimSuffix(viewBuilder.String(), "\n"), "\n")
	if len(linesList) > height {
		linesList = linesList[:height]
	}
	return strings.Join(linesList, "\n")
}

const (
	altRankW    = 4
	altProbW    = 8
	altDistW    = 12
	altLogitW   = 7
	altLogprobW = 10
	altOverhead = 13
)

func (m *Model) renderAlternativesView(width, height int) string {
	var sb strings.Builder
	activeLane := m.Runner.GetActiveLane()

	title := " Alternatives "
	if m.SelectedStepIdx != -1 {
		title = fmt.Sprintf(" Alternatives (Step %d) ", m.SelectedStepIdx)
	} else {
		title = " Alternatives (Next Step) "
	}
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	if activeLane == nil {
		return sb.String()
	}

	tokenW := width - (altRankW + altProbW + altDistW + altLogitW + altLogprobW + altOverhead)
	if tokenW < 6 {
		tokenW = 6
	}
	distW := altDistW

	m.updateAlternativesTable(tokenW, distW)

	tableHeight := height - 4
	if tableHeight < 1 {
		tableHeight = 1
	}
	m.Table.SetHeight(tableHeight)

	columns := []table.Column{
		{Title: "Rank", Width: altRankW},
		{Title: "Token text", Width: tokenW},
		{Title: "Prob", Width: altProbW},
		{Title: "Distribution", Width: altDistW},
		{Title: "Logit", Width: altLogitW},
		{Title: "Logprob", Width: altLogprobW},
	}
	m.Table.SetColumns(columns)

	tableWidth := altRankW + tokenW + altProbW + altDistW + altLogitW + altLogprobW + altOverhead
	if tableWidth > width {
		tableWidth = width
	}
	m.Table.SetWidth(tableWidth)

	sb.WriteString(m.Table.View())
	linesList := strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n")
	if len(linesList) > height {
		linesList = linesList[:height]
	}
	return strings.Join(linesList, "\n")
}

func (m *Model) updateAlternativesTable(args ...int) {
	activeLane := m.Runner.GetActiveLane()
	if activeLane == nil {
		m.Table.SetRows(nil)
		return
	}

	tokenW := 10
	distW := 12
	if len(args) >= 2 {
		tokenW = args[0]
		distW = args[1]
	}

	var alts []steerinspect.Alternative
	var err error
	if m.SelectedStepIdx == -1 {
		alts, err = m.getLiveAlternatives(activeLane)
		if err != nil {
			if m.LogBuf != nil {
				m.LogBuf.WriteLog(ds4api.LogError, fmt.Sprintf("TUI: failed to get live alternatives: %v\n", err))
			}
			m.Table.SetRows(nil)
			return
		}
	} else {
		alts = activeLane.Steps[m.SelectedStepIdx].Alternatives
	}

	rows := make([]table.Row, len(alts))
	for idx, alt := range alts {
		prob := math.Exp(float64(alt.Logprob))
		probStr := fmt.Sprintf("%.2f%%", prob*100)
		if prob < 0.001 {
			probStr = fmt.Sprintf("%.4f%%", prob*100)
		}

		// Escape special control and whitespace characters in token text.
		escapedText := alt.TokenText
		escapedText = strings.ReplaceAll(escapedText, " ", "·")
		escapedText = strings.ReplaceAll(escapedText, "\n", "\\n")
		escapedText = strings.ReplaceAll(escapedText, "\r", "\\r")
		escapedText = strings.ReplaceAll(escapedText, "\t", "\\t")

		// Truncate to maximum tokenW runes to prevent shifting the table layout.
		truncatedText := truncateRunes(escapedText, tokenW)

		rows[idx] = table.Row{
			fmt.Sprintf("%d", idx+1),
			truncatedText,
			probStr,
			renderProgressBar(prob, distW),
			fmt.Sprintf("%.2f", alt.Logit),
			fmt.Sprintf("%.4f", alt.Logprob),
		}
	}

	m.Table.SetRows(rows)

	// Styles matching our design system
	s := table.DefaultStyles()
	s.Cell = s.Cell.Foreground(ColorPrimary)
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(ColorBorder).
		BorderBottom(true).
		Bold(true).
		Foreground(ColorAccent)

	if m.Focus == FocusAlternatives {
		s.Selected = s.Selected.
			Foreground(ColorDark).
			Background(ColorAccent).
			Bold(true)
	} else {
		s.Selected = s.Selected.
			Foreground(ColorPrimary).
			Background(ColorSurface).
			Bold(false)
	}
	m.Table.SetStyles(s)

	// Sync table cursor with SelectedAltIdx (if valid)
	if m.SelectedAltIdx >= 0 && m.SelectedAltIdx < len(rows) {
		m.Table.SetCursor(m.SelectedAltIdx)
	} else {
		m.Table.SetCursor(0)
		m.SelectedAltIdx = 0
	}
}

// truncateRunes safely truncates a string to at most maxRunes, adding an ellipsis if truncated.
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) > maxRunes {
		if maxRunes > 3 {
			return string(runes[:maxRunes-3]) + "..."
		}
		return string(runes[:maxRunes])
	}
	return s
}
func (m *Model) renderMetricsLine(label, value string, highlight bool, width int) string {
	labelW := 24
	if width < 50 {
		labelW = width / 2
		if labelW < 12 {
			labelW = 12
		}
	}
	if labelW > width-4 {
		labelW = width - 4
		if labelW < 4 {
			labelW = 4
		}
	}

	var formattedLabel string
	if len([]rune(label)) > labelW {
		formattedLabel = truncateRunes(label, labelW)
	} else {
		formattedLabel = fmt.Sprintf("%-*s", labelW, label)
	}

	avail := width - labelW - 1
	if avail < 3 {
		avail = 3
	}

	formattedValue := truncateRunes(value, avail)

	style := PrimaryStyle
	if highlight {
		style = SteerHighlight
	}

	return style.Render(formattedLabel) + " " + style.Render(formattedValue) + "\n"
}

func (m *Model) renderMetricsView(args ...int) string {
	width := 60
	height := 10
	if len(args) == 1 {
		height = args[0]
	} else if len(args) >= 2 {
		width = args[0]
		height = args[1]
	}
	_ = height

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(" Metrics ") + "\n\n")

	activeLane := m.Runner.GetActiveLane()
	if activeLane == nil {
		return sb.String()
	}

	// Render normal single-lane step details and sparklines.
	var currentStep *steerinspect.Step
	stepIdx := m.SelectedStepIdx
	if stepIdx != -1 && stepIdx < len(activeLane.Steps) {
		currentStep = &activeLane.Steps[stepIdx]
	} else if len(activeLane.Steps) > 0 {
		stepIdx = len(activeLane.Steps) - 1
		currentStep = &activeLane.Steps[stepIdx]
	}

	// Always show selected step info fields at the top of the metrics view
	if currentStep != nil {
		sb.WriteString(m.renderMetricsLine("Selected Step Index:", fmt.Sprintf("%d (Pos %d)", stepIdx, currentStep.Pos), false, width))

		// Find selected token logprob
		var chosenLogprob float32
		var foundLogprob bool
		for _, alt := range currentStep.Alternatives {
			if alt.TokenID == currentStep.TokenID {
				chosenLogprob = alt.Logprob
				foundLogprob = true
				break
			}
		}
		tokenInfo := fmt.Sprintf("%d (%q)", currentStep.TokenID, currentStep.TokenText)
		sb.WriteString(m.renderMetricsLine("Selected Token:", tokenInfo, false, width))

		var logprobInfo string = "-"
		if foundLogprob {
			logprobInfo = fmt.Sprintf("%.4f", chosenLogprob)
		}
		sb.WriteString(m.renderMetricsLine("Selected Logprob:", logprobInfo, false, width))

		// Combine Entropy and Margin to save vertical space
		sb.WriteString(m.renderMetricsLine("Selected Entropy/Margin:", fmt.Sprintf("%.3f / %.3f", currentStep.Entropy, currentStep.Margin), false, width))

		if currentStep.SteerApplied != nil {
			sb.WriteString(m.renderMetricsLine("Selected Steer:", fmt.Sprintf("%s (FFN Scale: %.2f)",
				currentStep.SteerApplied.Vector, currentStep.SteerApplied.FFNScale), true, width))
		} else {
			sb.WriteString(m.renderMetricsLine("Selected Steer:", "-", false, width))
		}
	} else {
		sb.WriteString(m.renderMetricsLine("Selected Step Index:", "-", false, width))
		sb.WriteString(m.renderMetricsLine("Selected Token:", "-", false, width))
		sb.WriteString(m.renderMetricsLine("Selected Logprob:", "-", false, width))
		sb.WriteString(m.renderMetricsLine("Selected Entropy/Margin:", "-", false, width))
		sb.WriteString(m.renderMetricsLine("Selected Steer:", "-", false, width))
	}

	if height > 11 {
		sb.WriteString("\n")
	}

	// Always show the totals (Total tokens output, Last Token/Entropy)
	rate := m.tokenRate()
	rateStr := "-"
	if rate > 0 {
		rateStr = fmt.Sprintf("%.1f tok/s", rate)
	}
	sb.WriteString(m.renderMetricsLine("Tokens Output / Rate:", fmt.Sprintf("%d / %s", len(activeLane.Steps), rateStr), false, width))
	if len(activeLane.Steps) > 0 {
		last := activeLane.Steps[len(activeLane.Steps)-1]
		var lastLogprob float32
		var foundLastLogprob bool
		for _, alt := range last.Alternatives {
			if alt.TokenID == last.TokenID {
				lastLogprob = alt.Logprob
				foundLastLogprob = true
				break
			}
		}
		lastInfo := fmt.Sprintf("%d (%q)", last.TokenID, last.TokenText)
		sb.WriteString(m.renderMetricsLine("Last Token/Entropy:", fmt.Sprintf("%s / %.3f", lastInfo, last.Entropy), false, width))

		var lastLogprobInfo string = "-"
		if foundLastLogprob {
			lastLogprobInfo = fmt.Sprintf("%.4f", lastLogprob)
		}
		sb.WriteString(m.renderMetricsLine("Last Logprob:", lastLogprobInfo, false, width))
	} else {
		sb.WriteString(m.renderMetricsLine("Last Token/Entropy:", "-", false, width))
		sb.WriteString(m.renderMetricsLine("Last Logprob:", "-", false, width))
	}

	// 3. Render sparklines.
	if len(activeLane.Steps) > 0 {
		if height > 12 {
			sb.WriteString("\n")
		}
		// Logprob sparkline.
		var logprobs []float32
		var margins []float32
		for _, s := range activeLane.Steps {
			// Find chosen token logprob.
			var chosenLP float32 = -20.0
			for _, alt := range s.Alternatives {
				if alt.TokenID == s.TokenID {
					chosenLP = alt.Logprob
					break
				}
			}
			// Shift logprob so it is positive: [-20, 0] maps to [0, 20]
			shiftedLP := chosenLP + 20.0
			if shiftedLP < 0 {
				shiftedLP = 0
			}
			logprobs = append(logprobs, shiftedLP)
			margins = append(margins, s.Margin)
		}

		sparkWidth := width - 12
		if sparkWidth < 5 {
			sparkWidth = 5
		}

		sb.WriteString(MutedStyle.Render(fmt.Sprintf("%-10s", "Logprobs:")) + renderSparkline(logprobs, sparkWidth) + "\n")
		sb.WriteString(MutedStyle.Render(fmt.Sprintf("%-10s", "Margins:")) + renderSparkline(margins, sparkWidth) + "\n")
	}

	linesList := strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n")
	if len(linesList) > height {
		linesList = linesList[:height]
	}
	return strings.Join(linesList, "\n")
}

func (m *Model) renderLogsView(args ...int) string {
	width := 60
	height := 10
	if len(args) == 1 {
		height = args[0]
	} else if len(args) >= 2 {
		width = args[0]
		height = args[1]
	}

	var sb strings.Builder
	title := " Engine Logs "
	if m.LogsScrollOffset > 0 {
		title = fmt.Sprintf(" Engine Logs (PAUSED ↑%d) ", m.LogsScrollOffset)
	}
	sb.WriteString(TitleStyle.Render(title) + "\n\n")

	if m.LogBuf == nil {
		sb.WriteString(MutedStyle.Render("No log buffer configured."))
		return sb.String()
	}

	lines := m.LogBuf.GetLines()
	if len(lines) == 0 {
		sb.WriteString(MutedStyle.Render("No logs captured yet."))
		return sb.String()
	}

	availLines := height - 2
	if availLines <= 0 {
		availLines = 1
	}

	tailEnd := len(lines) - m.LogsScrollOffset
	if tailEnd < availLines {
		tailEnd = availLines
	}
	if tailEnd > len(lines) {
		tailEnd = len(lines)
	}
	startIdx := tailEnd - availLines
	if startIdx < 0 {
		startIdx = 0
	}

	for i := startIdx; i < tailEnd; i++ {
		truncated := truncateRunes(lines[i], width)
		sb.WriteString(PrimaryStyle.Render(truncated) + "\n")
	}

	linesList := strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n")
	if len(linesList) > height {
		linesList = linesList[:height]
	}
	return strings.Join(linesList, "\n")
}

// logsVisibleLines returns the number of log lines currently visible
// in the logs pane (height minus the title + blank lines).
func (m *Model) logsVisibleLines() int {
	if !m.ShowLogs || m.LogBuf == nil {
		return 1
	}
	statusView := m.renderStatusView()
	_, _, _, _, _, _, _, _, _, hLogs := GetLayoutDimensions(m.Width, m.Height, statusView, true)
	avail := (hLogs - 2) - 2 // outer border (-2) and inner title+blank (-2)
	if avail < 1 {
		avail = 1
	}
	return avail
}

// clampLogsOffset clamps m.LogsScrollOffset to [0, max(0, len(lines)-visible)].
func (m *Model) clampLogsOffset() {
	if m.LogBuf == nil {
		m.LogsScrollOffset = 0
		return
	}
	lines := m.LogBuf.GetLines()
	visible := m.logsVisibleLines()
	maxOffset := len(lines) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.LogsScrollOffset > maxOffset {
		m.LogsScrollOffset = maxOffset
	}
	if m.LogsScrollOffset < 0 {
		m.LogsScrollOffset = 0
	}
}

func renderSparkline(values []float32, width int) string {
	if len(values) == 0 {
		return ""
	}
	sl := sparkline.New(width, 1)
	float64s := make([]float64, len(values))
	for i, v := range values {
		float64s[i] = float64(v)
	}
	sl.PushAll(float64s)
	sl.DrawBraille()
	return strings.TrimSuffix(sl.View(), "\n")
}

func (m *Model) renderStatusView() string {
	// Status message fades after 5 seconds.
	status := ""
	if m.StatusMsg != "" && time.Since(m.StatusTime) < 5*time.Second {
		status = m.StatusMsg
	}

	barStyle := lipgloss.NewStyle().
		Background(ColorSurface).
		Foreground(ColorPrimary).
		Padding(0, 1)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Active Lane: %s | Model: %s", m.Runner.ActiveLaneID, m.ModelPath))
	if m.ModelName != "" {
		sb.WriteString(fmt.Sprintf(" (%s id=%d)", m.ModelName, m.ModelID))
	}
	if m.Generating {
		sb.WriteString(" | [GENERATING...]")
	}
	if status != "" {
		sb.WriteString(" | " + SteerHighlight.Render(status))
	}
	sb.WriteString("\n")
	sb.WriteString(MutedStyle.Render("Press '?' for help. Tab switches panes. Space decodes 1 token. Enter runs continuously."))

	return barStyle.Render(sb.String())
}

func (m *Model) openDiffPicker() {
	pairs := steerinspect.EligiblePairs(m.Runner.GetLanes())
	if len(pairs) == 0 {
		m.setStatus("Need at least two lanes to diff.")
		return
	}
	m.DiffPickerOpen = true
	m.DiffPickerIdx = 0
}

// renderProgressBar draws a smooth horizontal progress bar representing a probability value.
func renderProgressBar(prob float64, width int) string {
	if width <= 0 {
		return ""
	}
	if prob < 0 {
		prob = 0
	}
	if prob > 1 {
		prob = 1
	}

	if prob == 0 {
		return strings.Repeat(" ", width)
	}

	// Calculate total steps (8 per block character)
	totalSteps := float64(width * 8)
	steps := int(math.Round(prob * totalSteps))
	if steps == 0 && prob > 0 {
		steps = 1
	}

	fullBlocks := steps / 8
	rem := steps % 8

	var sb strings.Builder
	for i := 0; i < fullBlocks; i++ {
		sb.WriteRune('█')
	}

	if rem > 0 {
		// Fractional blocks: left-aligned blocks representing 1/8 to 7/8
		blocks := []rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉'}
		sb.WriteRune(blocks[rem])
	}

	runeCount := fullBlocks
	if rem > 0 {
		runeCount++
	}
	for runeCount < width {
		sb.WriteRune(' ')
		runeCount++
	}
	return sb.String()
}
