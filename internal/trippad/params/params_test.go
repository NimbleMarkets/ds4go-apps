package params

import (
	"math"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSetAndValidation(t *testing.T) {
	defs := []Param{{Name: "speed", Min: 0, Max: 2, Step: .1, Default: 1}}
	s, err := New(defs)
	if err != nil {
		t.Fatal(err)
	}
	defs[0].Name = "changed"
	for _, tc := range []struct{ in, want float32 }{{-3, 0}, {5, 2}, {.35, .35}} {
		got, err := s.Set("speed", tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("Set(%g)=%g,%v", tc.in, got, err)
		}
	}
	if s.Index("speed") != 0 || s.Index("missing") != -1 {
		t.Fatal("index lookup")
	}
	if _, err := s.Set("missing", 1); err == nil {
		t.Fatal("accepted unknown")
	}
	if _, err := s.Set("speed", float32(math.NaN())); err == nil {
		t.Fatal("accepted NaN")
	}
	for _, defs := range [][]Param{
		{{Name: "x", Min: 0, Max: 1, Step: 0}},
		{{Name: "x", Min: 0, Max: 1, Step: .1, Default: 2}},
		{{Name: "bad-name", Min: 0, Max: 1, Step: .1}},
		{{Name: "x", Min: 0, Max: 1, Step: .1}, {Name: "x", Min: 0, Max: 1, Step: .1}},
		make([]Param, 17),
	} {
		if _, err := New(defs); err == nil {
			t.Fatalf("accepted %+v", defs)
		}
	}
}
func TestPresetRoundTrip(t *testing.T) {
	p := Preset{Name: "custom", Shader: "return vec3<f32>(p_gain());", Params: []Param{{Name: "gain", Min: 0, Max: 1, Step: .01, Default: .5}}, Values: map[string]float32{"gain": .75}}
	path := filepath.Join(t.TempDir(), "saved")
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, p) {
		t.Fatalf("round trip: %+v", got)
	}
	p.Values["missing"] = 1
	if err := Save(path, p); err == nil {
		t.Fatal("accepted unknown value")
	}
	got, err = Load(path)
	if err != nil || got.Values["gain"] != .75 {
		t.Fatal("failed save corrupted preset")
	}
}
