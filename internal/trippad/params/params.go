// Package params defines trippad's ordered, named shader controls and presets.
package params

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxParams = 16

type Param struct {
	Name    string  `json:"name"`
	Min     float32 `json:"min"`
	Max     float32 `json:"max"`
	Step    float32 `json:"step"`
	Default float32 `json:"default"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

func finite(v float32) bool { return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) }

func Validate(defs []Param) error {
	if len(defs) > MaxParams {
		return fmt.Errorf("at most %d parameters allowed", MaxParams)
	}
	seen := map[string]bool{}
	for _, p := range defs {
		if !identifier.MatchString(p.Name) || strings.Contains(p.Name, "__") || seen[p.Name] {
			return fmt.Errorf("invalid or duplicate parameter name %q", p.Name)
		}
		seen[p.Name] = true
		if !finite(p.Min) || !finite(p.Max) || !finite(p.Step) || !finite(p.Default) || p.Min >= p.Max || p.Step <= 0 || p.Default < p.Min || p.Default > p.Max {
			return fmt.Errorf("invalid range, step or default for %q", p.Name)
		}
	}
	return nil
}

type Set struct {
	defs   []Param
	values [MaxParams]float32
}

func New(defs []Param) (*Set, error) {
	if err := Validate(defs); err != nil {
		return nil, err
	}
	s := &Set{defs: append([]Param(nil), defs...)}
	for i, p := range defs {
		s.values[i] = p.Default
	}
	return s, nil
}
func (s *Set) Definitions() []Param       { return append([]Param(nil), s.defs...) }
func (s *Set) Values() [MaxParams]float32 { return s.values }
func (s *Set) Index(name string) int {
	for i, p := range s.defs {
		if p.Name == name {
			return i
		}
	}
	return -1
}
func (s *Set) Named() map[string]float32 {
	m := map[string]float32{}
	for i, p := range s.defs {
		m[p.Name] = s.values[i]
	}
	return m
}
func (s *Set) Set(name string, v float32) (float32, error) {
	i := s.Index(name)
	if i < 0 {
		return 0, fmt.Errorf("unknown parameter %q", name)
	}
	if !finite(v) {
		return 0, fmt.Errorf("parameter value must be finite")
	}
	v = max(s.defs[i].Min, min(s.defs[i].Max, v))
	s.values[i] = v
	return v, nil
}

// Preset includes definitions so custom shaders can be restored independently.
type Preset struct {
	Name   string             `json:"name"`
	Shader string             `json:"shader"`
	Params []Param            `json:"params"`
	Values map[string]float32 `json:"values"`
}

func (p Preset) Validate() error {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Shader) == "" {
		return fmt.Errorf("preset name and shader are required")
	}
	s, err := New(p.Params)
	if err != nil {
		return err
	}
	for k, v := range p.Values {
		if _, err := s.Set(k, v); err != nil {
			return err
		}
	}
	return nil
}
func Filename(path string) string {
	if !strings.HasSuffix(path, ".trip.json") {
		path += ".trip.json"
	}
	return path
}
func Save(path string, p Preset) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("filename required")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	path = Filename(path)
	f, err := os.CreateTemp(filepath.Dir(path), ".trippad-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func Load(path string) (Preset, error) {
	var p Preset
	b, err := os.ReadFile(Filename(path))
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}
