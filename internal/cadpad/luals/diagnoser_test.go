package luals_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/luals"
)

func newDiagnoser(t *testing.T) *luals.Diagnoser {
	t.Helper()
	if _, err := exec.LookPath("lua-language-server"); err != nil {
		t.Skip("lua-language-server not on PATH")
	}
	ws := t.TempDir()
	defs := filepath.Join(ws, "..", "defs")
	if err := luals.WriteDefs(defs); err != nil {
		t.Fatalf("WriteDefs: %v", err)
	}
	// LuaLS' first response is slow (meta preload); allow generous time.
	d, err := luals.New(context.Background(), ws, defs, 60*time.Second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d == nil {
		t.Skip("lua-language-server reported absent")
	}
	t.Cleanup(func() { d.Close(context.Background()) })
	t.Cleanup(func() { _ = os.RemoveAll(defs) })
	return d
}

func TestCheck_FlagsSyntaxError(t *testing.T) {
	d := newDiagnoser(t)
	out := d.Check(context.Background(), "broken.lua", "local x = \n")
	if out == "" {
		t.Fatal("expected a diagnostic for a syntax error, got none")
	}
}

func TestCheck_CleanForValidSdfGlobal(t *testing.T) {
	d := newDiagnoser(t)
	// Global-form sdf usage must NOT produce "undefined global sdf": the stub
	// declares it. This proves the stub suppresses false positives.
	src := "local s = sdf.sphere(5)\nsdf.register(\"ball\", s)\n"
	out := d.Check(context.Background(), "ok.lua", src)
	if out != "" {
		t.Fatalf("expected no diagnostics for valid sdf usage, got:\n%s", out)
	}
}
