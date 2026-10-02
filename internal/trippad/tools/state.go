package tools

import (
	"fmt"
	"image"
	"maps"
	"math/rand/v2"
	"slices"
	"sync"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
)

type Renderer interface {
	Compile(shader.Source) error
	Render(shader.Source, [16]float32, float32, float32, uint32, int, int) (*image.NRGBA, error)
}

type Snapshot struct {
	Source      shader.Source      `json:"source"`
	Values      [16]float32        `json:"-"`
	Named       map[string]float32 `json:"values"`
	Time        float32            `json:"time"`
	Dt          float32            `json:"dt"`
	Frame       uint32             `json:"frame"`
	FPS         float64            `json:"fps"`
	Width       int                `json:"width"`
	Height      int                `json:"height"`
	Revision    uint64             `json:"-"`
	Performance Performance        `json:"performance"`
}

// Performance records wall times, not GPU timestamps or terminal display latency.
type Performance struct {
	RenderMS    float64 `json:"render_ms"`
	EncodeMS    float64 `json:"encode_ms"`
	UploadBytes int     `json:"upload_bytes"`
	SampledAt   int64   `json:"sampled_at"`
	Kitty       bool    `json:"kitty"`
	// Transport is what the last Kitty frame actually used: png, rgba, or shm.
	// With shm, UploadBytes is only the reference written to the terminal.
	Transport string `json:"transport,omitempty"`
}

type State struct {
	mu          sync.RWMutex
	source      shader.Source
	values      *params.Set
	revision    uint64
	t, dt       float32
	frame       uint32
	fps         float64
	w, h        int
	renderer    Renderer
	archive     func(params.Preset) error
	performance Performance
}

// SetArchive enables durable history for subsequent successful replacements.
// The callback must not call back into State.
func (s *State) SetArchive(save func(params.Preset) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archive = save
}

func preset(src shader.Source, values *params.Set) params.Preset {
	return params.Preset{Name: src.Name, Shader: src.ShadeBody, Params: values.Definitions(), Values: values.Named()}
}

// Checkpoint records current controls as well as source, for gallery browsing
// and clean shutdown. Shader replacements themselves save synchronously.
func (s *State) Checkpoint() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.archive == nil {
		return nil
	}
	return s.archive(preset(s.source, s.values))
}

func NewState(src shader.Source, r Renderer) (*State, error) {
	if r == nil {
		return nil, fmt.Errorf("renderer required")
	}
	if _, err := shader.BuildWGSL(src); err != nil {
		return nil, err
	}
	v, err := params.New(src.Params)
	if err != nil {
		return nil, err
	}
	src.Params = v.Definitions()
	return &State{source: src, values: v, renderer: r, w: 512, h: 320}, nil
}
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *State) snapshotLocked() Snapshot {
	src := s.source
	src.Params = s.values.Definitions()
	return Snapshot{Source: src, Values: s.values.Values(), Named: s.values.Named(), Time: s.t, Dt: s.dt, Frame: s.frame, FPS: s.fps, Width: s.w, Height: s.h, Revision: s.revision, Performance: s.performance}
}

func (s *State) RecordPerformance(p Performance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.performance = p
}

// Replace compiles before committing. A failed compile never changes live state.
// Concurrent edits supersede the pending replacement instead of being lost.
// Identical source and effective values skip compilation, history and revision changes.
func (s *State) Replace(src shader.Source, values map[string]float32, revision uint64) error {
	_, _, err := s.replace(src, values, revision)
	return err
}

type shaderChanges struct {
	Body   bool `json:"body"`
	Name   bool `json:"name"`
	Params bool `json:"params"`
	Values bool `json:"values"`
}

func (c shaderChanges) any() bool { return c.Body || c.Name || c.Params || c.Values }

func (s *State) replace(src shader.Source, values map[string]float32, revision uint64) (Snapshot, shaderChanges, error) {
	var changes shaderChanges
	v, err := params.New(src.Params)
	if err != nil {
		return Snapshot{}, changes, err
	}
	for k, n := range values {
		if _, err := v.Set(k, n); err != nil {
			return Snapshot{}, changes, err
		}
	}
	s.mu.RLock()
	if s.revision != revision {
		s.mu.RUnlock()
		return Snapshot{}, changes, fmt.Errorf("state changed while preparing shader; retry the edit")
	}
	changes = shaderChanges{
		Body:   src.ShadeBody != s.source.ShadeBody,
		Name:   src.Name != s.source.Name,
		Params: !slices.Equal(v.Definitions(), s.values.Definitions()),
		Values: !maps.Equal(v.Named(), s.values.Named()),
	}
	if !changes.any() {
		snap := s.snapshotLocked()
		s.mu.RUnlock()
		return snap, changes, nil
	}
	s.mu.RUnlock()
	if err := s.renderer.Compile(src); err != nil {
		return Snapshot{}, changes, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != revision {
		return Snapshot{}, changes, fmt.Errorf("state changed while compiling; retry the edit")
	}
	if s.archive != nil {
		// Preserve tuned controls on the old shader before switching. Persist
		// the new working version before publishing it so a crash can't erase
		// a shader that was reported as successfully installed.
		if err := s.archive(preset(s.source, s.values)); err != nil {
			return Snapshot{}, changes, fmt.Errorf("save previous shader to gallery: %w", err)
		}
		if err := s.archive(preset(src, v)); err != nil {
			return Snapshot{}, changes, fmt.Errorf("save shader to gallery: %w", err)
		}
	}
	src.Params = v.Definitions()
	s.source = src
	s.values = v
	s.revision++
	return s.snapshotLocked(), changes, nil
}
func (s *State) Set(name string, value float32) (float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.values.Set(name, value)
	if err == nil {
		s.revision++
	}
	return v, err
}
func (s *State) Nudge(index, steps int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defs := s.values.Definitions()
	if index < 0 || index >= len(defs) {
		return nil
	}
	p := defs[index]
	_, err := s.values.Set(p.Name, s.values.Values()[index]+float32(steps)*p.Step)
	if err == nil {
		s.revision++
	}
	return err
}
func (s *State) Randomize() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.values.Definitions() {
		s.values.Set(p.Name, p.Min+rand.Float32()*(p.Max-p.Min))
	}
	s.revision++
}
func (s *State) Clock(t, dt float32, frame uint32, fps float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.t = t
	s.dt = dt
	s.frame = frame
	s.fps = fps
}
func (s *State) Render(snap Snapshot, w, h int) (*image.NRGBA, error) {
	return s.renderer.Render(snap.Source, snap.Values, snap.Time, snap.Dt, snap.Frame, w, h)
}

// Viewport records the live render size so vision sees the same aspect ratio.
func (s *State) Viewport(w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.w, s.h = max(1, min(4096, w)), max(1, min(4096, h))
}
func (s *State) Save(path string) error {
	snap := s.Snapshot()
	return params.Save(path, params.Preset{Name: snap.Source.Name, Shader: snap.Source.ShadeBody, Params: snap.Source.Params, Values: snap.Named})
}
func (s *State) Load(path string) error {
	rev := s.Snapshot().Revision
	p, err := params.Load(path)
	if err != nil {
		return err
	}
	return s.Replace(shader.Source{Name: p.Name, ShadeBody: p.Shader, Params: p.Params}, p.Values, rev)
}
