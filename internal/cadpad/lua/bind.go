// Package lua provides a low-level gopher-lua binding to simplesdf for cadpad.
//
// Design philosophy (per request):
//   - Expose low-level primitives and operations directly.
//   - Do NOT provide high-level "helpful" functions (box with holes, etc.).
//   - The LLM is expected to build its own higher-level library over time
//     by writing reusable Lua functions in the project directory.
//
// The binding is intentionally minimal so that useful abstractions emerge
// from actual usage rather than being prematurely designed in Go.
package lua

import (
	"sort"

	"github.com/soypat/gsdf/gsdfaux/simplesdf"
	lua "github.com/yuin/gopher-lua"

	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
)

// RegisterSDF registers the low-level "sdf" module into the given Lua state
// so that it is available both as a global and (more importantly) via
// `require("sdf")` from Lua scripts.
//
// After calling this, Lua code can do:
//
//   local sdf = require("sdf")
//   local s = sdf.sphere(5)
//   local b = sdf.box(10, 8, 3, 0)
//   local u = s:union(b):translate(0, 0, 1.5)
//
//   sdf.register("my_part", u)

// sdfConstructors are the module-level constructor functions exposed as
// sdf.<name>. "register" is added per-state in RegisterSDF because it closes
// over the World/Renderer. Keep names in sync with luals/sdf.lua (drift test).
var sdfConstructors = map[string]lua.LGFunction{
	"sphere":   lSphere,
	"box":      lBox,
	"cylinder": lCylinder,
	"torus":    lTorus,
	"hexprism": lHexPrism,
	"triprism": lTriPrism,
	"boxframe": lBoxFrame,
}

// BindingNames returns every name exposed on the sdf module (constructors plus
// "register") and every SDF3 method, sorted. It is the source of truth for the
// luals/sdf.lua drift test.
func BindingNames() []string {
	names := make([]string, 0, len(sdfConstructors)+1+len(sdf3Methods))
	for k := range sdfConstructors {
		names = append(names, k)
	}
	names = append(names, "register")
	for k := range sdf3Methods {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func RegisterSDF(L *lua.LState, w *world.World, r *render.Renderer) {
	// Per-state metatable setup is required. gopher-lua keeps type
	// metatables in the per-LState registry, so we must ensure the
	// "sdf3" metatable (with its __index methods) exists for *this*
	// state every time we register the module.
	mt := L.NewTypeMetatable("sdf3")
	L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), sdf3Methods))

	funcs := make(map[string]lua.LGFunction, len(sdfConstructors)+1)
	for k, v := range sdfConstructors {
		funcs[k] = v
	}
	funcs["register"] = lRegister(w, r)
	mod := L.SetFuncs(L.NewTable(), funcs)

	// Make it available both ways:
	// 1. Global (convenient for tiny one-liners)
	// 2. Via require() — this is what the LLM is told to use in practice
	L.SetGlobal("sdf", mod)
	L.PreloadModule("sdf", func(L *lua.LState) int {
		L.Push(mod)
		return 1
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Constructors (return SDF3 userdata)
// ─────────────────────────────────────────────────────────────────────────────

func lSphere(L *lua.LState) int {
	r := L.CheckNumber(1)
	s := simplesdf.Sphere(float64(r))
	return pushSDF3(L, s)
}

func lBox(L *lua.LState) int {
	x := L.CheckNumber(1)
	y := L.CheckNumber(2)
	z := L.CheckNumber(3)
	round := float64(0)
	if L.GetTop() >= 4 {
		round = float64(L.CheckNumber(4))
	}
	s := simplesdf.Box(float64(x), float64(y), float64(z), round)
	return pushSDF3(L, s)
}

func lCylinder(L *lua.LState) int {
	r := L.CheckNumber(1)
	h := L.CheckNumber(2)
	round := float64(0)
	if L.GetTop() >= 3 {
		round = float64(L.CheckNumber(3))
	}
	s := simplesdf.Cylinder(float64(r), float64(h), round)
	return pushSDF3(L, s)
}

func lTorus(L *lua.LState) int {
	major := L.CheckNumber(1)
	minor := L.CheckNumber(2)
	s := simplesdf.Torus(float64(major), float64(minor))
	return pushSDF3(L, s)
}

func lHexPrism(L *lua.LState) int {
	face2face := L.CheckNumber(1)
	h := L.CheckNumber(2)
	s := simplesdf.HexPrism(float64(face2face), float64(h))
	return pushSDF3(L, s)
}

func lTriPrism(L *lua.LState) int {
	triH := L.CheckNumber(1)
	extrude := L.CheckNumber(2)
	s := simplesdf.TriPrism(float64(triH), float64(extrude))
	return pushSDF3(L, s)
}

func lBoxFrame(L *lua.LState) int {
	x := L.CheckNumber(1)
	y := L.CheckNumber(2)
	z := L.CheckNumber(3)
	thick := L.CheckNumber(4)
	s := simplesdf.BoxFrame(float64(x), float64(y), float64(z), float64(thick))
	return pushSDF3(L, s)
}

// ─────────────────────────────────────────────────────────────────────────────
// Methods on SDF3 userdata
// ─────────────────────────────────────────────────────────────────────────────

var sdf3Methods = map[string]lua.LGFunction{
	// Boolean ops
	"union":     lUnion,
	"diff":      lDiff,
	"intersect": lIntersect,
	"xor":       lXor,

	// Fluent smooth control
	"k": lK,

	// Transforms (preserve pending k)
	"translate": lTranslate,
	"scale":     lScale,
	"rotate":    lRotate,
	"rotate_x":  lRotateX,
	"rotate_y":  lRotateY,
	"rotate_z":  lRotateZ,

	// Modifiers
	"offset":   lOffset,
	"shell":    lShell,
	"elongate": lElongate,
}

// Boolean operations
func lUnion(L *lua.LState) int {
	self := checkSDF3(L, 1)
	others := make([]simplesdf.SDF3, 0, L.GetTop()-1)
	for i := 2; i <= L.GetTop(); i++ {
		others = append(others, checkSDF3(L, i))
	}
	result := self.Union(others...)
	return pushSDF3(L, result)
}

func lDiff(L *lua.LState) int {
	self := checkSDF3(L, 1)
	other := checkSDF3(L, 2)
	result := self.Diff(other)
	return pushSDF3(L, result)
}

func lIntersect(L *lua.LState) int {
	self := checkSDF3(L, 1)
	other := checkSDF3(L, 2)
	result := self.Intersect(other)
	return pushSDF3(L, result)
}

func lXor(L *lua.LState) int {
	self := checkSDF3(L, 1)
	other := checkSDF3(L, 2)
	result := self.Xor(other)
	return pushSDF3(L, result)
}

// K sets pending blend radius for next boolean
func lK(L *lua.LState) int {
	self := checkSDF3(L, 1)
	k := L.CheckNumber(2)
	result := self.K(float64(k))
	return pushSDF3(L, result)
}

// Transforms
func lTranslate(L *lua.LState) int {
	self := checkSDF3(L, 1)
	x := L.CheckNumber(2)
	y := L.CheckNumber(3)
	z := L.CheckNumber(4)
	result := self.Translate(float64(x), float64(y), float64(z))
	return pushSDF3(L, result)
}

func lScale(L *lua.LState) int {
	self := checkSDF3(L, 1)
	f := L.CheckNumber(2)
	result := self.Scale(float64(f))
	return pushSDF3(L, result)
}

func lRotate(L *lua.LState) int {
	self := checkSDF3(L, 1)
	rad := L.CheckNumber(2)
	ax := L.CheckNumber(3)
	ay := L.CheckNumber(4)
	az := L.CheckNumber(5)
	result := self.Rotate(float64(rad), float64(ax), float64(ay), float64(az))
	return pushSDF3(L, result)
}

func lRotateX(L *lua.LState) int {
	self := checkSDF3(L, 1)
	rad := L.CheckNumber(2)
	result := self.RotateX(float64(rad))
	return pushSDF3(L, result)
}

func lRotateY(L *lua.LState) int {
	self := checkSDF3(L, 1)
	rad := L.CheckNumber(2)
	result := self.RotateY(float64(rad))
	return pushSDF3(L, result)
}

func lRotateZ(L *lua.LState) int {
	self := checkSDF3(L, 1)
	rad := L.CheckNumber(2)
	result := self.RotateZ(float64(rad))
	return pushSDF3(L, result)
}

// Modifiers
func lOffset(L *lua.LState) int {
	self := checkSDF3(L, 1)
	d := L.CheckNumber(2)
	result := self.Offset(float64(d))
	return pushSDF3(L, result)
}

func lShell(L *lua.LState) int {
	self := checkSDF3(L, 1)
	t := L.CheckNumber(2)
	result := self.Shell(float64(t))
	return pushSDF3(L, result)
}

func lElongate(L *lua.LState) int {
	self := checkSDF3(L, 1)
	x := L.CheckNumber(2)
	y := L.CheckNumber(3)
	z := L.CheckNumber(4)
	result := self.Elongate(float64(x), float64(y), float64(z))
	return pushSDF3(L, result)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func pushSDF3(L *lua.LState, s simplesdf.SDF3) int {
	ud := L.NewUserData()
	ud.Value = s
	L.SetMetatable(ud, L.GetTypeMetatable("sdf3"))
	L.Push(ud)
	return 1
}

func checkSDF3(L *lua.LState, n int) simplesdf.SDF3 {
	ud := L.CheckUserData(n)
	if s, ok := ud.Value.(simplesdf.SDF3); ok {
		return s
	}
	L.ArgError(n, "sdf3 expected")
	return simplesdf.SDF3{}
}

// lRegister allows Lua to publish a named object into the cadpad World.
func lRegister(w *world.World, r *render.Renderer) lua.LGFunction {
	return func(L *lua.LState) int {
		name := L.CheckString(1)
		ud := L.CheckUserData(2)
		s, ok := ud.Value.(simplesdf.SDF3)
		if !ok {
			L.ArgError(2, "sdf3 expected")
		}
		// We use the low-level Create here so the LLM can name things.
		// For now we just directly insert (bypassing the high-level Create
		// which only supports a few shapes). This is intentional for the
		// "build your own library" approach.
		w.CreateFromSDF(name, s) // method added below
		if r != nil {
			r.Invalidate(name)
		}
		return 0
	}
}
