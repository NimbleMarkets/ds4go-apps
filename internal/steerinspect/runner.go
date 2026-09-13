package steerinspect

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
)

// RunnerOptions configures a Runner instance.
type RunnerOptions struct {
	TopK              int
	Temperature       float32
	TopP              float32
	MinP              float32
	Seed              uint64
	AllowAttnSteering bool
	CtxSize           int
}

// SteerArgs represents the JSON arguments for the model-callable steer tool.
type SteerArgs struct {
	Vector    string  `json:"vector"`
	Mode      string  `json:"mode"`
	FFNScale  float32 `json:"ffn_scale"`
	AttnScale float32 `json:"attn_scale"`
	Threshold float32 `json:"threshold"`
	Scope     string  `json:"scope"`
}

// Runner coordinates the multi-lane interactive generation environment.
type Runner struct {
	mu                sync.Mutex
	Engine            *ds4api.Engine
	Registry          *Registry
	Lanes             map[string]*Lane
	ActiveLaneID      string
	laneCounter       int
	TopK              int
	Temperature       float32
	TopP              float32
	MinP              float32
	Seed              uint64
	AllowAttnSteering bool
	CtxSize           int

	currentSteering map[string]*SteeringConfig
	decoders        map[string]*dsml.StreamDecoder
}

// NewRunner creates a new Runner instance.
func NewRunner(engine *ds4api.Engine, registry *Registry, opts RunnerOptions) *Runner {
	if opts.TopK <= 0 {
		opts.TopK = 20
	}
	return &Runner{
		Engine:            engine,
		Registry:          registry,
		Lanes:             make(map[string]*Lane),
		currentSteering:   make(map[string]*SteeringConfig),
		decoders:          make(map[string]*dsml.StreamDecoder),
		TopK:              opts.TopK,
		Temperature:       opts.Temperature,
		TopP:              opts.TopP,
		MinP:              opts.MinP,
		Seed:              opts.Seed,
		AllowAttnSteering: opts.AllowAttnSteering,
		CtxSize:           opts.CtxSize,
	}
}

// InitRootLane starts the first (unsteered) lane L1 using the prefilled prompt tokens.
func (r *Runner) InitRootLane(promptTokens *ds4api.Tokens) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	session, err := r.Engine.NewSession(r.CtxSize)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	if err := session.SyncTokens(promptTokens); err != nil {
		session.Close()
		return "", fmt.Errorf("sync prompt tokens: %w", err)
	}

	r.laneCounter++
	laneID := fmt.Sprintf("L%d", r.laneCounter)
	initialPos := session.Pos()

	lane := NewLane(laneID, "", 0, session, initialPos)
	r.Lanes[laneID] = lane
	r.ActiveLaneID = laneID

	r.decoders[laneID] = dsml.NewStreamDecoder(true)

	return laneID, nil
}

// BranchLane forks the selected lane at the specified token position.
func (r *Runner) BranchLane(parentID string, pos int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	parentLane, ok := r.Lanes[parentID]
	if !ok {
		return "", fmt.Errorf("lane %s not found", parentID)
	}

	// Save parent's current state.
	parentSnap, err := parentLane.Session.SaveSnapshot()
	if err != nil {
		return "", fmt.Errorf("save parent snapshot: %w", err)
	}

	// Rewind parent to pos, save the snapshot at pos, then restore parent.
	if err := parentLane.Session.RewindSynced(pos); err != nil {
		if restoreErr := parentLane.Session.LoadSnapshot(parentSnap); restoreErr != nil {
			return "", fmt.Errorf("rewind parent: %w; restore parent: %v", err, restoreErr)
		}
		return "", fmt.Errorf("rewind parent: %w", err)
	}
	branchSnap, err := parentLane.Session.SaveSnapshot()
	if err != nil {
		_ = parentLane.Session.LoadSnapshot(parentSnap)
		return "", fmt.Errorf("save branch snapshot: %w", err)
	}

	if err := parentLane.Session.LoadSnapshot(parentSnap); err != nil {
		return "", fmt.Errorf("restore parent: %w", err)
	}

	// Create new session for the branch.
	sessionB, err := r.Engine.NewSession(r.CtxSize)
	if err != nil {
		return "", fmt.Errorf("create branch session: %w", err)
	}

	if err := sessionB.LoadSnapshot(branchSnap); err != nil {
		sessionB.Close()
		return "", fmt.Errorf("load branch snapshot: %w", err)
	}

	// Find the active steering config at pos.
	var activeSteer *SteeringConfig
	for _, s := range parentLane.Steps {
		if s.Pos < pos {
			activeSteer = s.SteerApplied
		}
	}

	// Apply steering if active.
	if activeSteer != nil {
		resolvedFile, err := r.Registry.Resolve(activeSteer)
		if err == nil && resolvedFile != "" {
			_ = sessionB.SetDirectionalSteering(
				resolvedFile,
				ds4api.SteeringMode(activeSteer.Mode),
				activeSteer.FFNScale,
				activeSteer.AttnScale,
				activeSteer.Threshold,
				ds4api.SteeringScope(activeSteer.Scope),
			)
			r.currentSteering[fmt.Sprintf("L%d", r.laneCounter+1)] = activeSteer
		}
	}

	r.laneCounter++
	laneID := fmt.Sprintf("L%d", r.laneCounter)

	lane := NewLane(laneID, parentID, pos, sessionB, pos)

	// Copy parent steps up to pos.
	for _, s := range parentLane.Steps {
		if s.Pos < pos {
			lane.AddStep(s)
		}
	}

	r.Lanes[laneID] = lane
	r.ActiveLaneID = laneID

	r.decoders[laneID] = dsml.NewStreamDecoder(true)

	return laneID, nil
}

// GenerateOne generates a single token step. If forcedToken is not nil, it forces that token.
func (r *Runner) GenerateOne(laneID string, forcedToken *int) (Step, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	lane, ok := r.Lanes[laneID]
	if !ok {
		return Step{}, fmt.Errorf("lane %s not found", laneID)
	}

	pos := lane.Session.Pos()

	// Get alternative candidates from logits.
	scores, err := lane.Session.TopLogprobs(r.TopK)
	if err != nil {
		return Step{}, fmt.Errorf("get top logprobs: %w", err)
	}

	alts := make([]Alternative, len(scores))
	for i, s := range scores {
		txt, _ := r.Engine.TokenText(s.ID)
		alts[i] = Alternative{
			TokenID:   s.ID,
			TokenText: txt,
			Logit:     s.Logit,
			Logprob:   s.Logprob,
		}
	}

	entropy, margin := CalculateEntropyAndMargin(alts)

	// Select next token ID.
	var chosenToken int
	if forcedToken != nil {
		chosenToken = *forcedToken
	} else {
		if r.Temperature == 0 {
			chosenToken = lane.Session.Argmax()
		} else {
			chosenToken = lane.Session.Sample(r.Temperature, 0, r.TopP, r.MinP, &r.Seed)
		}
	}

	tokenText, _ := r.Engine.TokenText(chosenToken)

	// Get active steering configuration.
	activeSteer := r.currentSteering[laneID]
	var steerApplied *SteeringConfig
	if activeSteer != nil {
		steerApplied = &SteeringConfig{
			Vector:    activeSteer.Vector,
			Mode:      activeSteer.Mode,
			FFNScale:  activeSteer.FFNScale,
			AttnScale: activeSteer.AttnScale,
			Threshold: activeSteer.Threshold,
			Scope:     activeSteer.Scope,
		}
	}

	// Evaluate token to advance session.
	if err := lane.Session.Eval(chosenToken); err != nil {
		return Step{}, fmt.Errorf("eval token: %w", err)
	}

	step := Step{
		Pos:          pos,
		TokenID:      chosenToken,
		TokenText:    tokenText,
		Alternatives: alts,
		Entropy:      entropy,
		Margin:       margin,
		SteerApplied: steerApplied,
	}

	lane.AddStep(step)

	// Handle dynamic DSML tool call.
	if decoder, exists := r.decoders[laneID]; exists && tokenText != "" {
		events := decoder.Write(tokenText)
		for _, ev := range events {
			if ev.Type == dsml.EventToolCallEnd && ev.Name == "steer" {
				r.handleSteerToolCall(lane, ev.Arguments)
			}
		}
	}

	return step, nil
}

// ApplyManualSteering updates the directional steering for a lane.
func (r *Runner) ApplyManualSteering(laneID string, cfg *SteeringConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	lane, ok := r.Lanes[laneID]
	if !ok {
		return fmt.Errorf("lane %s not found", laneID)
	}

	if cfg == nil {
		err := lane.Session.SetDirectionalSteering("", 0, 0, 0, 0, ds4api.SteeringScopeOff)
		if err != nil {
			return err
		}
		r.currentSteering[laneID] = nil
		return nil
	}

	resolvedFile, err := r.Registry.Resolve(cfg)
	if err != nil {
		return err
	}

	err = lane.Session.SetDirectionalSteering(
		resolvedFile,
		ds4api.SteeringMode(cfg.Mode),
		cfg.FFNScale,
		cfg.AttnScale,
		cfg.Threshold,
		ds4api.SteeringScope(cfg.Scope),
	)
	if err != nil {
		return err
	}

	r.currentSteering[laneID] = cfg
	return nil
}

// InjectText tokenizes and evaluates text directly into a lane, appending steps.
func (r *Runner) InjectText(lane *Lane, text string) error {
	tokens, err := r.Engine.TokenizeText(text)
	if err != nil {
		return err
	}
	defer tokens.Free()

	slice := tokens.Slice()
	for _, tok := range slice {
		pos := lane.Session.Pos()
		tokenText, _ := r.Engine.TokenText(tok)

		if err := lane.Session.Eval(tok); err != nil {
			return err
		}

		step := Step{
			Pos:          pos,
			TokenID:      tok,
			TokenText:    tokenText,
			Alternatives: nil,
			Entropy:      0,
			Margin:       0,
			SteerApplied: r.currentSteering[lane.ID],
		}
		lane.AddStep(step)
	}
	return nil
}

func (r *Runner) handleSteerToolCall(lane *Lane, argsJSON string) {
	var args SteerArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		resp := fmt.Sprintf(`{"error": "invalid arguments: %v"}`, err)
		resBlock, _ := dsml.RenderToolResult(resp)
		_ = r.InjectText(lane, resBlock)
		return
	}

	mode, err := ParseSteeringMode(args.Mode)
	if err != nil {
		resp := fmt.Sprintf(`{"error": "invalid mode: %v"}`, err)
		resBlock, _ := dsml.RenderToolResult(resp)
		_ = r.InjectText(lane, resBlock)
		return
	}

	scope, err := ParseSteeringScope(args.Scope)
	if err != nil {
		resp := fmt.Sprintf(`{"error": "invalid scope: %v"}`, err)
		resBlock, _ := dsml.RenderToolResult(resp)
		_ = r.InjectText(lane, resBlock)
		return
	}

	cfg := &SteeringConfig{
		Vector:    args.Vector,
		Mode:      mode,
		FFNScale:  args.FFNScale,
		AttnScale: args.AttnScale,
		Threshold: args.Threshold,
		Scope:     scope,
	}

	if cfg.AttnScale != 0 && !r.AllowAttnSteering {
		resp := `{"error": "attention steering is not allowed in this session"}`
		resBlock, _ := dsml.RenderToolResult(resp)
		_ = r.InjectText(lane, resBlock)
		return
	}

	resolvedFile, err := r.Registry.Resolve(cfg)
	if err != nil {
		resp := fmt.Sprintf(`{"error": "failed to resolve vector registry: %v"}`, err)
		resBlock, _ := dsml.RenderToolResult(resp)
		_ = r.InjectText(lane, resBlock)
		return
	}

	err = lane.Session.SetDirectionalSteering(
		resolvedFile,
		ds4api.SteeringMode(cfg.Mode),
		cfg.FFNScale,
		cfg.AttnScale,
		cfg.Threshold,
		ds4api.SteeringScope(cfg.Scope),
	)
	if err != nil {
		resp := fmt.Sprintf(`{"error": "failed to apply steering parameters: %v"}`, err)
		resBlock, _ := dsml.RenderToolResult(resp)
		_ = r.InjectText(lane, resBlock)
		return
	}

	r.currentSteering[lane.ID] = cfg
	resBlock, _ := dsml.RenderToolResult(`{"ok": true}`)
	_ = r.InjectText(lane, resBlock)
}

// GetLanes returns the map of active lanes.
func (r *Runner) GetLanes() map[string]*Lane {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Lanes
}

// GetActiveLane returns the currently active lane.
func (r *Runner) GetActiveLane() *Lane {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Lanes[r.ActiveLaneID]
}

// GetCurrentSteering returns the active steering configuration for the given lane.
func (r *Runner) GetCurrentSteering(laneID string) *SteeringConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.currentSteering[laneID]
}

// Close closes all sessions associated with the runner.
func (r *Runner) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, lane := range r.Lanes {
		if lane.Session != nil {
			lane.Session.Close()
			lane.Session = nil
		}
	}
}

// Reset closes all active sessions and clears the lanes mapping.
func (r *Runner) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, lane := range r.Lanes {
		if lane.Session != nil {
			lane.Session.Close()
			lane.Session = nil
		}
	}
	r.Lanes = make(map[string]*Lane)
	r.ActiveLaneID = ""
	r.laneCounter = 0
	r.currentSteering = make(map[string]*SteeringConfig)
	r.decoders = make(map[string]*dsml.StreamDecoder)
}
