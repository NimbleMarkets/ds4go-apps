package steerinspect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

func TestCalculateEntropyAndMargin(t *testing.T) {
	alts := []Alternative{
		{TokenID: 1, TokenText: "a", Logprob: -0.1},
		{TokenID: 2, TokenText: "b", Logprob: -1.5},
		{TokenID: 3, TokenText: "c", Logprob: -3.0},
	}

	entropy, margin := CalculateEntropyAndMargin(alts)

	if entropy <= 0 {
		t.Errorf("expected positive entropy, got %f", entropy)
	}

	expectedMargin := float32(-0.1 - (-1.5))
	if margin != expectedMargin {
		t.Errorf("expected margin %f, got %f", expectedMargin, margin)
	}
}

func TestRegistry(t *testing.T) {
	tmp, err := os.MkdirTemp("", "dir-steering-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmp)

	// Create a mock vectors.json
	registryJSON := `{
		"verbosity": {
			"file": "verbosity.f32",
			"description": "Verbosity steering",
			"default_ffn": 1.0,
			"max_ffn": 2.0,
			"model_callable": true,
			"allowed_modes": ["ablation", "additive"]
		}
	}`
	err = os.WriteFile(filepath.Join(tmp, "vectors.json"), []byte(registryJSON), 0644)
	if err != nil {
		t.Fatalf("failed to write vectors.json: %v", err)
	}

	// Write mock vector file
	err = os.WriteFile(filepath.Join(tmp, "verbosity.f32"), []byte("fake vector content"), 0644)
	if err != nil {
		t.Fatalf("failed to write verbosity.f32: %v", err)
	}

	reg, err := LoadRegistry(tmp)
	if err != nil {
		t.Fatalf("LoadRegistry failed: %v", err)
	}

	if _, ok := reg.Vectors["verbosity"]; !ok {
		t.Fatalf("expected 'verbosity' vector in registry")
	}

	// Test Resolve success
	cfg := &SteeringConfig{
		Vector:   "verbosity",
		Mode:     SteeringAdditive,
		FFNScale: 1.5,
	}
	resolved, err := reg.Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	expectedPath := filepath.Join(tmp, "verbosity.f32")
	if resolved != expectedPath {
		t.Errorf("expected resolved path %q, got %q", expectedPath, resolved)
	}

	// Test Resolve exceeding MaxFFN
	cfg.FFNScale = 2.5
	_, err = reg.Resolve(cfg)
	if err == nil {
		t.Errorf("expected error for FFNScale exceeding MaxFFN")
	}

	// Test Resolve disallowed mode
	cfg.FFNScale = 1.0
	cfg.Mode = SteeringThreshold
	_, err = reg.Resolve(cfg)
	if err == nil {
		t.Errorf("expected error for disallowed mode")
	}
}

func TestRunnerMockWorkflow(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	ds4api.SetDefaultLibrary(lib)

	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close()

	// Initialize empty registry
	reg := &Registry{
		Dir:     ".",
		Vectors: make(map[string]RegistryVector),
	}

	opts := RunnerOptions{
		TopK:        5,
		Temperature: 0,
		CtxSize:     128,
	}
	runner := NewRunner(engine, reg, opts)
	defer runner.Close()

	// Prepare initial prompt
	promptTokens, err := engine.TokenizeText("System: You are helpful. User: Hello.")
	if err != nil {
		t.Fatalf("TokenizeText: %v", err)
	}
	defer promptTokens.Free()

	// 1. Initialize root lane
	rootID, err := runner.InitRootLane(promptTokens)
	if err != nil {
		t.Fatalf("InitRootLane: %v", err)
	}

	lane := runner.GetActiveLane()
	if lane == nil || lane.ID != rootID {
		t.Fatalf("expected active lane to be root lane %s", rootID)
	}

	// 2. Generate a token
	step, err := runner.GenerateOne(rootID, nil)
	if err != nil {
		t.Fatalf("GenerateOne: %v", err)
	}

	if len(lane.Steps) != 1 {
		t.Errorf("expected 1 step in lane, got %d", len(lane.Steps))
	}
	if step.Pos != lane.InitialPos {
		t.Errorf("expected step position %d, got %d", lane.InitialPos, step.Pos)
	}

	// 3. Branching
	branchPos := lane.InitialPos + 1
	branchID, err := runner.BranchLane(rootID, branchPos)
	if err != nil {
		t.Fatalf("BranchLane: %v", err)
	}

	branchedLane := runner.Lanes[branchID]
	if branchedLane == nil {
		t.Fatalf("branched lane was not created")
	}

	if len(branchedLane.Steps) != 1 {
		t.Errorf("expected branched lane to inherit 1 step, got %d", len(branchedLane.Steps))
	}

	// Generate on branched lane
	_, err = runner.GenerateOne(branchID, nil)
	if err != nil {
		t.Fatalf("GenerateOne on branched lane: %v", err)
	}

	if len(branchedLane.Steps) != 2 {
		t.Errorf("expected branched lane to have 2 steps, got %d", len(branchedLane.Steps))
	}

	// 4. Rewinding
	err = runner.ApplyManualSteering(rootID, nil)
	if err != nil {
		t.Fatalf("ApplyManualSteering: %v", err)
	}

	err = lane.Rewind(lane.InitialPos)
	if err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	if len(lane.Steps) != 0 {
		t.Errorf("expected lane steps to be truncated, got %d", len(lane.Steps))
	}
}
