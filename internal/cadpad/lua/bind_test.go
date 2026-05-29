package lua

import (
	"sort"
	"testing"
)

func TestBindingNames_CoversConstructorsAndMethods(t *testing.T) {
	got := BindingNames()
	if !sort.StringsAreSorted(got) {
		t.Fatalf("BindingNames() must be sorted, got %v", got)
	}
	want := []string{
		// constructors
		"box", "boxframe", "cylinder", "hexprism", "register", "sphere",
		"torus", "triprism",
		// SDF3 methods
		"diff", "elongate", "intersect", "k", "offset", "rotate", "rotate_x",
		"rotate_y", "rotate_z", "scale", "shell", "translate", "union", "xor",
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("count mismatch: got %d %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
