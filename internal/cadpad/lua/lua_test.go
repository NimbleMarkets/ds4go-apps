package lua

import (
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	// render is passed to NewState but not required for the binding logic itself
	// (lRegister only touches the World). We pass nil for pure binding tests.
	_ "github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
)

func TestLuaBasicPrimitivesAndRegister(t *testing.T) {
	w := world.NewWorld()
	st := NewState(w, nil)
	defer st.Close()

	script := `
		local sdf = require("sdf")
		local s = sdf.sphere(5)
		sdf.register("my_sphere", s)
	`
	if err := st.DoString(script); err != nil {
		t.Fatalf("DoString failed: %v", err)
	}

	if !w.Has("my_sphere") {
		t.Fatal("expected 'my_sphere' to be registered in world")
	}
	if len(w.Names()) != 1 {
		t.Fatalf("expected 1 object, got %d", len(w.Names()))
	}
}

func TestLuaChainingAndMultipleOps(t *testing.T) {
	w := world.NewWorld()
	st := NewState(w, nil)
	defer st.Close()

	script := `
		local sdf = require("sdf")
		local box = sdf.box(4, 3, 2)
		local cyl = sdf.cylinder(1, 5)
		local u = box:union(cyl):translate(0, 0, 1.5):k(0.2)
		sdf.register("combined", u)

		local diff = sdf.sphere(3):diff(sdf.box(1,1,10))
		sdf.register("holed", diff)
	`
	if err := st.DoString(script); err != nil {
		t.Fatalf("chaining script failed: %v", err)
	}

	if !w.Has("combined") || !w.Has("holed") {
		t.Fatalf("expected combined + holed objects, got names: %v", w.Names())
	}
	if len(w.Names()) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(w.Names()))
	}
}

func TestLuaAllPrimitives(t *testing.T) {
	w := world.NewWorld()
	st := NewState(w, nil)
	defer st.Close()

	prims := []string{"sphere", "box", "cylinder", "torus", "hexprism", "triprism", "boxframe"}
	for i, p := range prims {
		script := `
			local sdf = require("sdf")
			local s
			if "` + p + `" == "sphere" then s = sdf.sphere(2)
			elseif "` + p + `" == "box" then s = sdf.box(2,2,2)
			elseif "` + p + `" == "cylinder" then s = sdf.cylinder(1, 3)
			elseif "` + p + `" == "torus" then s = sdf.torus(3, 0.8)
			elseif "` + p + `" == "hexprism" then s = sdf.hexprism(2, 3)
			elseif "` + p + `" == "triprism" then s = sdf.triprism(2, 3)
			elseif "` + p + `" == "boxframe" then s = sdf.boxframe(4,4,4,0.5)
			end
			sdf.register("p` + string(rune('0'+i)) + `", s)
		`
		if err := st.DoString(script); err != nil {
			t.Fatalf("primitive %s failed: %v", p, err)
		}
	}

	if len(w.Names()) != len(prims) {
		t.Fatalf("expected %d registered primitives, got %d: %v", len(prims), len(w.Names()), w.Names())
	}
}

func TestLuaLuaLevelFunctionsAndLoops(t *testing.T) {
	// This is the key "build your own library" capability.
	w := world.NewWorld()
	st := NewState(w, nil)
	defer st.Close()

	script := `
		local sdf = require("sdf")
		function make_row(count, spacing)
			local parts = {}
			for i=0,count-1 do
				local s = sdf.sphere(0.8):translate(i*spacing, 0, 0)
				table.insert(parts, s)
			end
			local row = parts[1]
			for i=2,#parts do row = row:union(parts[i]) end
			return row
		end

		local r = make_row(4, 2.5)
		sdf.register("sphere_row", r)
	`
	if err := st.DoString(script); err != nil {
		t.Fatalf("higher-order Lua script failed: %v", err)
	}
	if !w.Has("sphere_row") {
		t.Fatal("function-built object not registered")
	}
}

func TestLuaErrorOnBadArgs(t *testing.T) {
	w := world.NewWorld()
	st := NewState(w, nil)
	defer st.Close()

	// Missing required arg to box
	err := st.DoString(`
		local sdf = require("sdf")
		local b = sdf.box(1)   -- missing y,z
	`)
	if err == nil {
		t.Fatal("expected error for insufficient args to box")
	}
	if !strings.Contains(err.Error(), "bad argument") && !strings.Contains(err.Error(), "number expected") {
		t.Logf("got error (acceptable): %v", err)
	}
}

func TestLuaRegisterReplacesAndSetsCurrent(t *testing.T) {
	w := world.NewWorld()
	st := NewState(w, nil)
	defer st.Close()

	script := `
		local sdf = require("sdf")
		sdf.register("item", sdf.sphere(1))
		sdf.register("item", sdf.box(5,1,1))  -- replace
	`
	if err := st.DoString(script); err != nil {
		t.Fatalf("replace script: %v", err)
	}
	if len(w.Names()) != 1 {
		t.Fatalf("expected single 'item' after replace, got %d", len(w.Names()))
	}
	if w.Current() != "item" {
		t.Errorf("expected current=item after register, got %q", w.Current())
	}
}
