// Package harness provides a thin registration layer so cadpad's geometry
// tools can be easily wired into any ds4go ToolRegistry (TUI or headless
// tool-server scenarios). It owns the binding of JSON schemas to the
// handler funcs that close over a World + Renderer.
package harness

import (
	"context"
	"encoding/json"
	"fmt"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/tools"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
)

// RegisterAll binds every cadpad tool into the supplied registry.
// The handlers close over the provided World (state) and Renderer (preview).
// Call once after creating both.
func RegisterAll(reg *ds4.ToolRegistry, w *world.World, r *render.Renderer) error {
	if reg == nil || w == nil || r == nil {
		return fmt.Errorf("harness: nil reg/world/renderer")
	}
	handlers := tools.Registry()
	schemas := tools.Schemas()
	if len(schemas) != len(handlers) {
		// Defensive; schemas and registry keys must stay in sync.
		return fmt.Errorf("harness: schema/handler count mismatch (%d vs %d)", len(schemas), len(handlers))
	}

	for _, sch := range schemas {
		fn := handlers[sch.Name]
		if fn == nil {
			return fmt.Errorf("harness: no handler for schema %s", sch.Name)
		}
		wrapped := func(ctx context.Context, raw json.RawMessage) (string, error) {
			return fn(ctx, w, r, raw)
		}
		if err := reg.RegisterFunc(sch, wrapped); err != nil {
			return fmt.Errorf("register %s: %w", sch.Name, err)
		}
	}
	return nil
}

// MustRegisterAll is RegisterAll that panics on error (convenience for mains).
func MustRegisterAll(reg *ds4.ToolRegistry, w *world.World, r *render.Renderer) {
	if err := RegisterAll(reg, w, r); err != nil {
		panic(err)
	}
}
