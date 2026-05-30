package luals_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/lua"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/luals"
)

func TestStubCoversAllBindings(t *testing.T) {
	stub := luals.Defs()
	for _, name := range lua.BindingNames() {
		// Each binding must appear as an actual declaration: `function
		// sdf.<name>(` (constructor/register) or `function SDF3:<name>(`
		// (method). Matching the declaration — not a loose substring — means a
		// name that is merely a prefix of another (rotate vs rotate_x) cannot
		// pass on the longer one's text.
		ctor := "function sdf." + name + "("
		method := "function SDF3:" + name + "("
		if !strings.Contains(stub, ctor) && !strings.Contains(stub, method) {
			t.Errorf("sdf.lua stub is missing a declaration for binding %q (add it; see bind.go)", name)
		}
	}
}

func TestWriteDefs(t *testing.T) {
	dir := t.TempDir()
	if err := luals.WriteDefs(dir); err != nil {
		t.Fatalf("WriteDefs: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sdf.lua"))
	if err != nil {
		t.Fatalf("read written stub: %v", err)
	}
	if string(data) != luals.Defs() {
		t.Fatal("written stub does not match embedded defs")
	}
}
