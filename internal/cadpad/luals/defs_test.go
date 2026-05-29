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
		// Each binding appears as `sdf.<name>` (constructor/register) or
		// `SDF3:<name>` (method) in the stub.
		if !strings.Contains(stub, "sdf."+name) && !strings.Contains(stub, ":"+name) {
			t.Errorf("sdf.lua stub is missing binding %q (add it; see bind.go)", name)
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
