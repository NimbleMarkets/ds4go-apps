package library

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
)

func TestImmutableHistoryDeduplicatesAndReopens(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := params.Preset{Name: "first", Shader: "return vec3<f32>(1.0);", Values: map[string]float32{}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := s.Save(p); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	entries, warnings, err := s.List()
	if err != nil || len(warnings) != 0 || len(entries) != 1 {
		t.Fatalf("duplicate history entries: %d, %v, %v", len(entries), warnings, err)
	}
	old := entries[0]
	p.Shader = "return vec3<f32>(0.0);"
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	reopened, _ := New(s.Dir)
	entries, _, err = reopened.List()
	if err != nil || len(entries) != 2 || entries[0].Preset.Shader != p.Shader {
		t.Fatal("versions don't survive reopening in newest-first order")
	}
	restored, err := params.Load(old.Path)
	if err != nil || restored.Shader != old.Preset.Shader {
		t.Fatal("older shader was overwritten")
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "broken.trip.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, warnings, err = s.List()
	if err != nil || len(entries) != 2 || len(warnings) != 1 {
		t.Fatal("one damaged file hid healthy gallery entries")
	}
}
