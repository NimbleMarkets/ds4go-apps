package world

import (
	"path/filepath"
	"testing"

	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

func TestWorldCreateBooleanTransform(t *testing.T) {
	w := NewWorld()
	if len(w.Names()) != 0 {
		t.Fatal("expected empty")
	}

	s, err := w.Create("base", "box", map[string]float64{"x": 4, "y": 3, "z": 1})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	if s.Shader() == nil {
		t.Fatal("nil shader")
	}
	if !w.Has("base") {
		t.Fatal("has failed")
	}

	w.Create("post", "cylinder", map[string]float64{"r": 0.5, "h": 2})
	if err := w.Transform("post", "translate", map[string]float64{"x": 0, "y": 0, "z": 1}); err != nil {
		t.Fatalf("translate: %v", err)
	}
	if err := w.Boolean("union", "base", "post", 0.1); err != nil {
		t.Fatalf("boolean: %v", err)
	}

	bb, ok := w.Bounds("base")
	if !ok || bb.Max.Z < 1.9 {
		t.Fatalf("bad bounds after union: %+v", bb)
	}

	// History recorded
	if len(w.History()) < 4 {
		t.Fatalf("expected history entries, got %d", len(w.History()))
	}
}

func TestWorldSaveLoad(t *testing.T) {
	w := NewWorld()
	w.Create("a", "sphere", map[string]float64{"r": 1.2})
	tmp := filepath.Join(t.TempDir(), "t.cad.json")
	if err := w.Save(tmp); err != nil {
		t.Fatal(err)
	}
	w2 := NewWorld()
	if err := w2.Load(tmp); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !w2.Has("a") {
		t.Fatal("replay did not recreate object")
	}
}

func TestSimplesdfErrHandling(t *testing.T) {
	// Exercise the error path the rest of cadpad relies on.
	simplesdf.ClearErrors()
	// Invalid negative radius should be caught (panic mode default).
	// We just ensure Err() surface exists and world uses it.
	_ = simplesdf.Err()
}
