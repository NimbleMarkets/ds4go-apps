package steerinspect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// SteeringMode reflects the C API activation steering mode.
type SteeringMode int

const (
	SteeringAblation  SteeringMode = SteeringMode(ds4api.SteeringAblation)
	SteeringThreshold SteeringMode = SteeringMode(ds4api.SteeringThreshold)
	SteeringAdditive  SteeringMode = SteeringMode(ds4api.SteeringAdditive)
)

// String returns the string representation of SteeringMode.
func (m SteeringMode) String() string {
	switch m {
	case SteeringAblation:
		return "ablation"
	case SteeringThreshold:
		return "threshold"
	case SteeringAdditive:
		return "additive"
	default:
		return fmt.Sprintf("unknown(%d)", m)
	}
}

// ParseSteeringMode parses m into SteeringMode.
func ParseSteeringMode(m string) (SteeringMode, error) {
	switch m {
	case "ablation":
		return SteeringAblation, nil
	case "threshold":
		return SteeringThreshold, nil
	case "additive":
		return SteeringAdditive, nil
	default:
		return SteeringAblation, fmt.Errorf("unknown steering mode: %s", m)
	}
}

// SteeringScope reflects the C API dynamic steering lifetime scope.
type SteeringScope int

const (
	SteeringScopeNextMessage SteeringScope = SteeringScope(ds4api.SteeringScopeNextMessage)
	SteeringScopeUntilRevert SteeringScope = SteeringScope(ds4api.SteeringScopeUntilRevert)
	SteeringScopeOff         SteeringScope = SteeringScope(ds4api.SteeringScopeOff)
)

// String returns the string representation of SteeringScope.
func (s SteeringScope) String() string {
	switch s {
	case SteeringScopeNextMessage:
		return "next_message"
	case SteeringScopeUntilRevert:
		return "until_revert"
	case SteeringScopeOff:
		return "off"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// ParseSteeringScope parses s into SteeringScope.
func ParseSteeringScope(s string) (SteeringScope, error) {
	switch s {
	case "next_message":
		return SteeringScopeNextMessage, nil
	case "until_revert":
		return SteeringScopeUntilRevert, nil
	case "off":
		return SteeringScopeOff, nil
	default:
		return SteeringScopeNextMessage, fmt.Errorf("unknown steering scope: %s", s)
	}
}

// SteeringConfig captures dynamic steering configurations for FFI.
type SteeringConfig struct {
	Vector    string // Name from vectors.json registry, or absolute file path
	Mode      SteeringMode
	FFNScale  float32
	AttnScale float32
	Threshold float32 // CAST threshold
	Scope     SteeringScope
}

// RegistryVector is the JSON configuration structure for a single steering direction.
type RegistryVector struct {
	File          string   `json:"file"`
	Description   string   `json:"description"`
	DefaultFFN    float32  `json:"default_ffn"`
	MaxFFN        float32  `json:"max_ffn"`
	ModelCallable bool     `json:"model_callable"`
	AllowedModes  []string `json:"allowed_modes"`
}

// Registry is a parsed vectors.json map.
type Registry struct {
	Dir     string
	Vectors map[string]RegistryVector
}

// LoadRegistry parses vectors.json in dir, falling back to ~/.ds4/dir-steering if empty.
func LoadRegistry(dir string) (*Registry, error) {
	if dir == "" {
		// Default path search
		dir = "./dir-steering"
		if _, err := os.Stat(filepath.Join(dir, "vectors.json")); err != nil {
			home, err := os.UserHomeDir()
			if err == nil {
				dir = filepath.Join(home, ".ds4", "dir-steering")
			}
		}
	}

	jsonPath := filepath.Join(dir, "vectors.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty registry gracefully
			return &Registry{Dir: dir, Vectors: make(map[string]RegistryVector)}, nil
		}
		return nil, fmt.Errorf("ds4: load registry %s: %w", jsonPath, err)
	}

	var vectors map[string]RegistryVector
	if err := json.Unmarshal(data, &vectors); err != nil {
		return nil, fmt.Errorf("ds4: parse registry %s: %w", jsonPath, err)
	}

	return &Registry{Dir: dir, Vectors: vectors}, nil
}

// Resolve returns the absolute file path and validated parameters for dynamic steering.
func (r *Registry) Resolve(cfg *SteeringConfig) (string, error) {
	if r == nil || len(r.Vectors) == 0 {
		// No registry: treat Vector as raw file path
		return cfg.Vector, nil
	}

	vec, ok := r.Vectors[cfg.Vector]
	if !ok {
		// Not in registry: check if it is a raw file path that exists
		if _, err := os.Stat(cfg.Vector); err == nil {
			return cfg.Vector, nil
		}
		return "", fmt.Errorf("unknown steering vector %q", cfg.Vector)
	}

	// Verify allowed modes
	modeStr := cfg.Mode.String()
	modeAllowed := false
	for _, m := range vec.AllowedModes {
		if m == modeStr {
			modeAllowed = true
			break
		}
	}
	if !modeAllowed && len(vec.AllowedModes) > 0 {
		return "", fmt.Errorf("mode %q not allowed for vector %q (allowed: %v)", modeStr, cfg.Vector, vec.AllowedModes)
	}

	// Enforce FFN clamps
	if vec.MaxFFN > 0 {
		scale := cfg.FFNScale
		if scale < 0 {
			scale = -scale
		}
		if scale > vec.MaxFFN {
			return "", fmt.Errorf("ffn_scale %f exceeds max_ffn %f for vector %q", cfg.FFNScale, vec.MaxFFN, cfg.Vector)
		}
	}

	// Check absolute path
	filePath := vec.File
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(r.Dir, filePath)
	}

	return filePath, nil
}
