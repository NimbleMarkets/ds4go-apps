// Package luals wires lua-language-server into cadpad so generated Lua is
// diagnosed against the real sdf API before it is executed.
package luals

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed sdf.lua
var sdfStub string

// Defs returns the embedded sdf type-stub source.
func Defs() string { return sdfStub }

// WriteDefs writes the embedded stub into dir (creating dir) as sdf.lua. dir is
// passed to lua-language-server as a workspace library so the sdf API resolves.
func WriteDefs(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("luals: mkdir defs dir: %w", err)
	}
	path := filepath.Join(dir, "sdf.lua")
	if err := os.WriteFile(path, []byte(sdfStub), 0o644); err != nil {
		return fmt.Errorf("luals: write %s: %w", path, err)
	}
	return nil
}
