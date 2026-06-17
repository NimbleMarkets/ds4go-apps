// Package world provides the core persistent CAD state for cadpad:
// named simplesdf.SDF3 objects, selection, operation history for undo/replay,
// bounding-box caches, and save/load via replayable operation log (JSON).
package world

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/soypat/geometry/ms3"
	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// Projection identifies a 2D orthographic view of a 3D SDF.
type Projection string

const (
	ProjXY Projection = "xy" // top view, looking down -Z
	ProjXZ Projection = "xz" // front view, looking down -Y ?
	ProjYZ Projection = "yz" // side view
)

// OpKind enumerates the high-level atomic operations (for history + save/load).
type OpKind string

const (
	OpCreate     OpKind = "create"
	OpBoolean    OpKind = "boolean"
	OpTransform  OpKind = "transform"
	OpGroup      OpKind = "group"
	OpDelete     OpKind = "delete"
	OpSetCurrent OpKind = "set_current"
	// Future: OpLoad etc are not recorded.
)

// OpRecord is one serializable step in the construction history.
type OpRecord struct {
	Kind      OpKind          `json:"kind"`
	Timestamp time.Time       `json:"ts"`
	Args      json.RawMessage `json:"args"`
	Note      string          `json:"note,omitempty"`
}

// CreateArgs for OpCreate.
type CreateArgs struct {
	Name   string             `json:"name"`
	Shape  string             `json:"shape"` // sphere, box, cylinder, torus, ...
	Params map[string]float64 `json:"params"`
}

// BooleanArgs for OpBoolean.
type BooleanArgs struct {
	Op          string  `json:"op"` // union, diff, intersect, xor
	Target      string  `json:"target"`
	Source      string  `json:"source"`
	BlendRadius float64 `json:"blend_radius,omitempty"`
}

// TransformArgs for OpTransform.
type TransformArgs struct {
	Name string             `json:"name"`
	Op   string             `json:"op"` // translate, rotate, scale, ...
	Args map[string]float64 `json:"args"`
}

// GroupArgs for OpGroup (creates a union under new name).
type GroupArgs struct {
	Name  string   `json:"name"`
	Items []string `json:"items"`
}

// Meta holds per-object metadata (lightweight, survives save/load).
type Meta struct {
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Desc    string    `json:"desc,omitempty"`
	// SourceOp records which high-level op created it (for UI).
	SourceOp string `json:"source_op,omitempty"`
}

// World is the mutable CAD document. All access is thread-safe via mu for
// LLM tool handlers that may be called from the toolloop goroutine while
// the TUI renders.
type World struct {
	mu sync.RWMutex

	objs    map[string]simplesdf.SDF3
	order   []string // stable insertion/display order
	meta    map[string]Meta
	curr    string             // selected object name or ""
	hist    []OpRecord         // replayable history (excludes transient preview)
	bbCache map[string]ms3.Box // cached bounds; invalidated on replace

	// lastPreview holds the most recent RenderPreview result for TUI consumption.
	lastPreview struct {
		Name string
		Proj Projection
		ImgW int
		ImgH int
		OK   bool
		Err  string
		At   time.Time
	}
}

// NewWorld returns an empty world with no objects.
func NewWorld() *World {
	return &World{
		objs:    make(map[string]simplesdf.SDF3),
		meta:    make(map[string]Meta),
		bbCache: make(map[string]ms3.Box),
	}
}

// Clone returns a deep-ish copy (SDF3s are immutable so share is fine).
func (w *World) Clone() *World {
	w.mu.RLock()
	defer w.mu.RUnlock()
	c := NewWorld()
	c.objs = make(map[string]simplesdf.SDF3, len(w.objs))
	for k, v := range w.objs {
		c.objs[k] = v
	}
	c.order = append([]string(nil), w.order...)
	c.meta = make(map[string]Meta, len(w.meta))
	for k, v := range w.meta {
		c.meta[k] = v
	}
	c.curr = w.curr
	c.hist = append([]OpRecord(nil), w.hist...)
	c.bbCache = make(map[string]ms3.Box, len(w.bbCache))
	for k, v := range w.bbCache {
		c.bbCache[k] = v
	}
	c.lastPreview = w.lastPreview
	return c
}

// Names returns a copy of the stable object name list.
func (w *World) Names() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]string, len(w.order))
	copy(out, w.order)
	return out
}

// Has reports whether name exists.
func (w *World) Has(name string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, ok := w.objs[name]
	return ok
}

// Get returns the SDF3 and a copy of its metadata (ok=false if missing).
func (w *World) Get(name string) (simplesdf.SDF3, Meta, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	s, ok := w.objs[name]
	if !ok {
		return simplesdf.SDF3{}, Meta{}, false
	}
	m := w.meta[name]
	return s, m, true
}

// Current returns the selected name (may be "").
func (w *World) Current() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.curr
}

// CreateFromSDF is a low-level entry point intended for use by the Lua
// binding. It allows injecting an already-built simplesdf.SDF3 under a name.
// Unlike the high-level Create(), this does not attempt to interpret shape/params.
func (w *World) CreateFromSDF(name string, s simplesdf.SDF3) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.objs[name] = s
	w.meta[name] = Meta{
		Created:  time.Now().UTC(),
		Updated:  time.Now().UTC(),
		SourceOp: "lua",
	}
	if _, exists := w.orderIdx(name); !exists {
		w.order = append(w.order, name)
	}
	delete(w.bbCache, name)
	w.curr = name

	// Record a special op so history is still somewhat useful
	w.hist = append(w.hist, OpRecord{
		Kind:      "lua_create",
		Timestamp: time.Now().UTC(),
		Args:      mustJSON(map[string]string{"name": name}),
		Note:      "created via Lua",
	})
}

// SetCurrent selects an existing object (no-op and false if missing).
func (w *World) SetCurrent(name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if name != "" {
		if _, ok := w.objs[name]; !ok {
			return false
		}
	}
	w.curr = name
	w.hist = append(w.hist, OpRecord{
		Kind:      OpSetCurrent,
		Timestamp: time.Now().UTC(),
		Args:      mustJSON(map[string]string{"name": name}),
	})
	return true
}

// Bounds returns the cached (or freshly computed) axis-aligned bounding box.
func (w *World) Bounds(name string) (ms3.Box, bool) {
	w.mu.RLock()
	s, ok := w.objs[name]
	if !ok {
		w.mu.RUnlock()
		return ms3.Box{}, false
	}
	if bb, hit := w.bbCache[name]; hit {
		w.mu.RUnlock()
		return bb, true
	}
	w.mu.RUnlock()

	// Compute outside lock to avoid holding during potential heavy Bounds().
	bb := s.Shader().Bounds()

	w.mu.Lock()
	w.bbCache[name] = bb
	w.mu.Unlock()
	return bb, true
}

// InvalidateBB drops a cached box (call after any replace that may change extent).
func (w *World) InvalidateBB(name string) {
	w.mu.Lock()
	delete(w.bbCache, name)
	w.mu.Unlock()
}

// Describe returns a human summary of current world state (for DescribeState tool + UI).
func (w *World) Describe() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if len(w.order) == 0 {
		return "empty world (no objects)"
	}
	cur := w.curr
	if cur == "" {
		cur = "(none)"
	}
	return fmt.Sprintf("%d objects; current=%s\nobjects: %v", len(w.order), cur, w.order)
}

// GetPreview returns a snapshot of the last RenderPreview call (for TUI polling).
func (w *World) GetPreview() (name string, proj Projection, imgW, imgH int, ok bool, errStr string, at time.Time) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	lp := w.lastPreview
	return lp.Name, lp.Proj, lp.ImgW, lp.ImgH, lp.OK, lp.Err, lp.At
}

// SetPreview records the outcome of a RenderPreview tool invocation.
func (w *World) SetPreview(name string, proj Projection, imgW, imgH int, ok bool, errStr string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastPreview = struct {
		Name string
		Proj Projection
		ImgW int
		ImgH int
		OK   bool
		Err  string
		At   time.Time
	}{name, proj, imgW, imgH, ok, errStr, time.Now().UTC()}
}

// --- Mutation API (used by tools + internal) ---
// These methods record to history so Save/Load can replay.

func (w *World) record(kind OpKind, args any, note string) {
	w.hist = append(w.hist, OpRecord{
		Kind:      kind,
		Timestamp: time.Now().UTC(),
		Args:      mustJSON(args),
		Note:      note,
	})
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// Create inserts a new named primitive (replaces if exists). Records OpCreate.
func (w *World) Create(name, shape string, params map[string]float64) (simplesdf.SDF3, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var s simplesdf.SDF3
	switch shape {
	case "sphere":
		r := params["r"]
		if r <= 0 {
			r = 1
		}
		s = simplesdf.Sphere(r)
	case "box":
		x := params["x"]
		y := params["y"]
		z := params["z"]
		if x <= 0 {
			x = 1
		}
		if y <= 0 {
			y = 1
		}
		if z <= 0 {
			z = 1
		}
		round := params["round"]
		s = simplesdf.Box(x, y, z, round)
	case "cylinder":
		r := params["r"]
		h := params["h"]
		if r <= 0 {
			r = 1
		}
		if h <= 0 {
			h = 2
		}
		round := params["round"]
		s = simplesdf.Cylinder(r, h, round)
	case "torus":
		maj := params["major"]
		min := params["minor"]
		if maj <= 0 {
			maj = 2
		}
		if min <= 0 {
			min = 0.5
		}
		s = simplesdf.Torus(maj, min)
	default:
		return simplesdf.SDF3{}, fmt.Errorf("unknown shape %q (supported: sphere,box,cylinder,torus)", shape)
	}
	if err := simplesdf.Err(); err != nil {
		simplesdf.ClearErrors()
		return simplesdf.SDF3{}, fmt.Errorf("simplesdf: %w", err)
	}

	w.objs[name] = s
	w.meta[name] = Meta{Created: time.Now().UTC(), Updated: time.Now().UTC(), SourceOp: "create:" + shape}
	// maintain order
	if _, exists := w.orderIdx(name); !exists {
		w.order = append(w.order, name)
	}
	delete(w.bbCache, name)
	w.record(OpCreate, CreateArgs{Name: name, Shape: shape, Params: params}, "")
	w.curr = name
	return s, nil
}

func (w *World) orderIdx(name string) (int, bool) {
	for i, n := range w.order {
		if n == name {
			return i, true
		}
	}
	return -1, false
}

// Boolean performs target = target op source (with optional blend). Replaces target.
func (w *World) Boolean(op, target, source string, blend float64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	t, ok := w.objs[target]
	if !ok {
		return fmt.Errorf("target %q not found", target)
	}
	s, ok := w.objs[source]
	if !ok {
		return fmt.Errorf("source %q not found", source)
	}

	var res simplesdf.SDF3
	switch op {
	case "union", "u":
		if blend > 0 {
			res = t.K(blend).Union(s.K(blend))
		} else {
			res = t.Union(s)
		}
	case "diff", "difference", "d", "subtract":
		if blend > 0 {
			res = t.K(blend).Diff(s.K(blend))
		} else {
			res = t.Diff(s)
		}
	case "intersect", "i", "and":
		if blend > 0 {
			res = t.K(blend).Intersect(s.K(blend))
		} else {
			res = t.Intersect(s)
		}
	case "xor":
		res = t.Xor(s)
	default:
		return fmt.Errorf("unknown boolean op %q (union,diff,intersect,xor)", op)
	}
	if err := simplesdf.Err(); err != nil {
		simplesdf.ClearErrors()
		return fmt.Errorf("simplesdf boolean: %w", err)
	}

	w.objs[target] = res
	m := w.meta[target]
	m.Updated = time.Now().UTC()
	w.meta[target] = m
	delete(w.bbCache, target)
	w.record(OpBoolean, BooleanArgs{Op: op, Target: target, Source: source, BlendRadius: blend}, "")
	w.curr = target
	return nil
}

// Transform applies an in-place transform to name (translate, rotate*, scale, ...).
func (w *World) Transform(name, op string, args map[string]float64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	s, ok := w.objs[name]
	if !ok {
		return fmt.Errorf("object %q not found", name)
	}

	var res simplesdf.SDF3
	switch op {
	case "translate", "move":
		res = s.Translate(args["x"], args["y"], args["z"])
	case "scale":
		f := args["factor"]
		if f == 0 {
			f = 1
		}
		res = s.Scale(f)
	case "rotate", "rotate_axis":
		rad := args["radians"]
		ax, ay, az := args["ax"], args["ay"], args["az"]
		res = s.Rotate(rad, ax, ay, az)
	case "rotate_x":
		res = s.RotateX(args["radians"])
	case "rotate_y":
		res = s.RotateY(args["radians"])
	case "rotate_z":
		res = s.RotateZ(args["radians"])
	case "offset":
		res = s.Offset(args["delta"])
	case "shell":
		res = s.Shell(args["thickness"])
	default:
		return fmt.Errorf("unknown transform %q", op)
	}
	if err := simplesdf.Err(); err != nil {
		simplesdf.ClearErrors()
		return fmt.Errorf("simplesdf transform: %w", err)
	}

	w.objs[name] = res
	m := w.meta[name]
	m.Updated = time.Now().UTC()
	w.meta[name] = m
	delete(w.bbCache, name)
	w.record(OpTransform, TransformArgs{Name: name, Op: op, Args: args}, "")
	return nil
}

// Group creates a new named union of the listed items (copies them via union).
func (w *World) Group(name string, items []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(items) == 0 {
		return fmt.Errorf("group requires at least one item")
	}
	var acc simplesdf.SDF3
	var first bool
	for _, it := range items {
		s, ok := w.objs[it]
		if !ok {
			return fmt.Errorf("item %q not found", it)
		}
		if !first {
			acc = s
			first = true
			continue
		}
		acc = acc.Union(s)
	}
	if err := simplesdf.Err(); err != nil {
		simplesdf.ClearErrors()
		return fmt.Errorf("simplesdf group: %w", err)
	}
	w.objs[name] = acc
	w.meta[name] = Meta{Created: time.Now().UTC(), Updated: time.Now().UTC(), SourceOp: "group"}
	if _, exists := w.orderIdx(name); !exists {
		w.order = append(w.order, name)
	}
	delete(w.bbCache, name)
	w.record(OpGroup, GroupArgs{Name: name, Items: items}, "")
	w.curr = name
	return nil
}

// Delete removes an object. If it was current, current becomes "" or another.
func (w *World) Delete(name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.objs, name)
	delete(w.meta, name)
	delete(w.bbCache, name)
	if idx, ok := w.orderIdx(name); ok {
		w.order = append(w.order[:idx], w.order[idx+1:]...)
	}
	if w.curr == name {
		if len(w.order) > 0 {
			w.curr = w.order[0]
		} else {
			w.curr = ""
		}
	}
	w.record(OpDelete, map[string]string{"name": name}, "")
}

// History returns a copy of the recorded operation log (for save + debug UI).
func (w *World) History() []OpRecord {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]OpRecord, len(w.hist))
	copy(out, w.hist)
	return out
}

// Save writes the replayable history as JSON array to filename.
func (w *World) Save(filename string) error {
	h := w.History()
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal history: %w", err)
	}
	return os.WriteFile(filename, b, 0644)
}

// Load clears the world and replays a saved history file (JSON []OpRecord).
// It uses the World's own mutators so new objects get correct SDFs.
func (w *World) Load(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var recs []OpRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		return fmt.Errorf("unmarshal history: %w", err)
	}

	// Start fresh (but keep a marker in history that we loaded).
	w.mu.Lock()
	w.objs = make(map[string]simplesdf.SDF3)
	w.order = nil
	w.meta = make(map[string]Meta)
	w.bbCache = make(map[string]ms3.Box)
	w.curr = ""
	oldHist := w.hist
	w.hist = nil
	w.mu.Unlock()

	for _, r := range recs {
		switch r.Kind {
		case OpCreate:
			var a CreateArgs
			if err := json.Unmarshal(r.Args, &a); err != nil {
				return fmt.Errorf("replay create: bad args: %w", err)
			}
			if _, err := w.Create(a.Name, a.Shape, a.Params); err != nil {
				return fmt.Errorf("replay create %s: %w", a.Name, err)
			}
		case OpBoolean:
			var a BooleanArgs
			if err := json.Unmarshal(r.Args, &a); err != nil {
				return fmt.Errorf("replay boolean: bad args: %w", err)
			}
			if err := w.Boolean(a.Op, a.Target, a.Source, a.BlendRadius); err != nil {
				return fmt.Errorf("replay boolean: %w", err)
			}
		case OpTransform:
			var a TransformArgs
			if err := json.Unmarshal(r.Args, &a); err != nil {
				return fmt.Errorf("replay transform: bad args: %w", err)
			}
			if err := w.Transform(a.Name, a.Op, a.Args); err != nil {
				return fmt.Errorf("replay transform %s: %w", a.Name, err)
			}
		case OpGroup:
			var a GroupArgs
			if err := json.Unmarshal(r.Args, &a); err != nil {
				return fmt.Errorf("replay group: bad args: %w", err)
			}
			if err := w.Group(a.Name, a.Items); err != nil {
				return fmt.Errorf("replay group %s: %w", a.Name, err)
			}
		case OpSetCurrent:
			var m map[string]string
			if err := json.Unmarshal(r.Args, &m); err != nil {
				return fmt.Errorf("replay set_current: bad args: %w", err)
			}
			w.SetCurrent(m["name"])
		case OpDelete:
			var m map[string]string
			if err := json.Unmarshal(r.Args, &m); err != nil {
				return fmt.Errorf("replay delete: bad args: %w", err)
			}
			w.Delete(m["name"])
		}
	}

	// Append a load marker (not part of replayed ops for re-saving).
	w.mu.Lock()
	w.hist = append(oldHist, OpRecord{Kind: "load", Timestamp: time.Now().UTC(), Note: "loaded " + filename})
	w.mu.Unlock()
	return nil
}

// Clear removes all objects and history (fresh slate).
func (w *World) Clear() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.objs = make(map[string]simplesdf.SDF3)
	w.order = nil
	w.meta = make(map[string]Meta)
	w.bbCache = make(map[string]ms3.Box)
	w.curr = ""
	w.hist = nil
	w.lastPreview = struct {
		Name string
		Proj Projection
		ImgW int
		ImgH int
		OK   bool
		Err  string
		At   time.Time
	}{}
}

// SortedNames returns names in lexicographic order (for stable UI lists).
func (w *World) SortedNames() []string {
	ns := w.Names()
	sort.Strings(ns)
	return ns
}
