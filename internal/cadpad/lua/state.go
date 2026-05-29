package lua

import (
	lua "github.com/yuin/gopher-lua"

	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
)

// State wraps a gopher-lua LState with cadpad context (World + Renderer).
type State struct {
	L *lua.LState
	w *world.World
	r *render.Renderer
}

// NewState creates a new Lua state pre-loaded with the low-level "sdf" module
// and any other safe base modules we decide to expose.
func NewState(w *world.World, r *render.Renderer) *State {
	L := lua.NewState(lua.Options{
		// Start conservative. We can tune registry/callstack later.
		CallStackSize: 256,
		RegistrySize:  1024 * 32,
	})

	s := &State{
		L: L,
		w: w,
		r: r,
	}

	// Register our low-level geometry binding.
	// This is intentionally minimal — the LLM builds higher-level helpers itself.
	RegisterSDF(L, w, r)

	// Future: we can add safe versions of basic libraries here if desired.
	// For now we leave the default Lua environment (math, table, string, etc.)
	// but will restrict filesystem access at the tool layer, not inside Lua.

	return s
}

// Close releases the underlying Lua state.
func (s *State) Close() {
	if s.L != nil {
		s.L.Close()
	}
}

// DoString executes Lua code in this state.
func (s *State) DoString(script string) error {
	return s.L.DoString(script)
}

// DoFile executes a Lua file.
func (s *State) DoFile(path string) error {
	return s.L.DoFile(path)
}
