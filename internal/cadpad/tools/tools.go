// Package tools implements the LLM-callable atomic operations for cadpad.
// Each exported func matches the high-level CAD modeling verbs requested:
// CreatePrimitive, BooleanOp, Transform, Group, ExportSTL, RenderPreview,
// DescribeState, GetBoundingBox, SaveWorld, LoadWorld.
//
// Handlers are designed to be registered via harness and operate on a
// shared *world.World. They return short human/LLM-friendly result strings.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/render"
	"github.com/NimbleMarkets/ds4go-apps/internal/cadpad/world"
	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// Schemas returns the full set of cadpad tool schemas (for harness or direct reg).
func Schemas() []ds4.ToolSchema {
	return []ds4.ToolSchema{
		{
			Name:        "cad_create",
			Description: "Create a named primitive (sphere, box, cylinder, torus). Replaces existing name. Sets as current.",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{
					"name":{"type":"string","description":"Unique object name"},
					"shape":{"type":"string","enum":["sphere","box","cylinder","torus"]},
					"params":{"type":"object","additionalProperties":{"type":"number"},"description":"shape-specific params e.g. {\"r\":1.5} for sphere, {\"x\":2,\"y\":3,\"z\":1} for box"}
				},
				"required":["name","shape"]
			}`),
		},
		{
			Name:        "cad_boolean",
			Description: "Boolean combine target = target OP source. Ops: union, diff, intersect, xor. Optional blend_radius for smooth.",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{
					"op":{"type":"string","enum":["union","diff","intersect","xor"]},
					"target":{"type":"string"},
					"source":{"type":"string"},
					"blend_radius":{"type":"number","description":">0 enables smooth blend (mm or model units)"}
				},
				"required":["op","target","source"]
			}`),
		},
		{
			Name:        "cad_transform",
			Description: "Apply a transform to an existing object in-place. Ops: translate, scale, rotate_x/y/z, offset, shell.",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{
					"name":{"type":"string"},
					"op":{"type":"string"},
					"args":{"type":"object","additionalProperties":{"type":"number"}}
				},
				"required":["name","op"]
			}`),
		},
		{
			Name:        "cad_group",
			Description: "Create a new named object that is the union of listed existing objects.",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{
					"name":{"type":"string"},
					"items":{"type":"array","items":{"type":"string"}}
				},
				"required":["name","items"]
			}`),
		},
		{
			Name:        "cad_export_stl",
			Description: "Export named object to STL file. ResolutionDivisions controls fineness (default 512).",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{
					"name":{"type":"string"},
					"filename":{"type":"string"},
					"resolution_divisions":{"type":"integer","minimum":64,"maximum":4096}
				},
				"required":["name","filename"]
			}`),
		},
		{
			Name:        "cad_export_3mf",
			Description: "Export named object to a 3MF file (modern STL replacement: smaller, carries units + name, accepted by all slicers). ResolutionDivisions controls fineness (default 512).",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{
					"name":{"type":"string"},
					"filename":{"type":"string"},
					"resolution_divisions":{"type":"integer","minimum":64,"maximum":4096}
				},
				"required":["name","filename"]
			}`),
		},
		{
			Name:        "cad_render_preview",
			Description: "Render an orthographic preview of object (xy/xz/yz). Updates the TUI viewport. Fast path for iteration.",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{
					"name":{"type":"string"},
					"projection":{"type":"string","enum":["xy","xz","yz"]},
					"width":{"type":"integer","description":"target pixel width hint"},
					"height":{"type":"integer","description":"target pixel height hint"}
				},
				"required":["name","projection"]
			}`),
		},
		{
			Name:        "cad_describe",
			Description: "Return a text summary of the entire world state, current selection, and object list.",
			Parameters:  mustSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "cad_bbox",
			Description: "Return the axis-aligned bounding box of a named object (min/max corners).",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{"name":{"type":"string"}},
				"required":["name"]
			}`),
		},
		{
			Name:        "cad_save",
			Description: "Save the construction history (replayable) to a .cad.json file.",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{"filename":{"type":"string"}},
				"required":["filename"]
			}`),
		},
		{
			Name:        "cad_load",
			Description: "Load and replay a saved .cad.json history, replacing current world contents.",
			Parameters: mustSchema(`{
				"type":"object",
				"properties":{"filename":{"type":"string"}},
				"required":["filename"]
			}`),
		},
	}
}

func mustSchema(s string) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(s))
}

// Handler is the signature expected by ds4.ToolFunc / harness.
type Handler func(ctx context.Context, w *world.World, r *render.Renderer, args json.RawMessage) (string, error)

// Registry returns a map of name -> handler (used by harness to bind schemas).
func Registry() map[string]Handler {
	return map[string]Handler{
		"cad_create":         CreatePrimitive,
		"cad_boolean":        BooleanOp,
		"cad_transform":      Transform,
		"cad_group":          Group,
		"cad_export_stl":     ExportSTL,
		"cad_export_3mf":     Export3MF,
		"cad_render_preview": RenderPreview,
		"cad_describe":       DescribeState,
		"cad_bbox":           GetBoundingBox,
		"cad_save":           SaveWorld,
		"cad_load":           LoadWorld,
	}
}

// --- Individual handlers ----------------------------------------------------

func CreatePrimitive(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Name   string             `json:"name"`
		Shape  string             `json:"shape"`
		Params map[string]float64 `json:"params"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	if a.Name == "" {
		return "", fmt.Errorf("name required")
	}
	if a.Params == nil {
		a.Params = map[string]float64{}
	}
	s, err := w.Create(a.Name, a.Shape, a.Params)
	if err != nil {
		return "", err
	}
	if r != nil {
		r.Invalidate(a.Name)
	}
	bb, _ := w.Bounds(a.Name)
	_ = s // keep for future use if needed
	return fmt.Sprintf("created %s (%s) bounds=[%.3f,%.3f]x[%.3f,%.3f]x[%.3f,%.3f]", a.Name, a.Shape, bb.Min.X, bb.Max.X, bb.Min.Y, bb.Max.Y, bb.Min.Z, bb.Max.Z), nil
}

func BooleanOp(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Op          string  `json:"op"`
		Target      string  `json:"target"`
		Source      string  `json:"source"`
		BlendRadius float64 `json:"blend_radius"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	if err := w.Boolean(a.Op, a.Target, a.Source, a.BlendRadius); err != nil {
		return "", err
	}
	if r != nil {
		r.Invalidate(a.Target)
	}
	return fmt.Sprintf("boolean %s %s %s (blend=%.3f) OK", a.Op, a.Target, a.Source, a.BlendRadius), nil
}

func Transform(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Name string             `json:"name"`
		Op   string             `json:"op"`
		Args map[string]float64 `json:"args"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	if a.Args == nil {
		a.Args = map[string]float64{}
	}
	if err := w.Transform(a.Name, a.Op, a.Args); err != nil {
		return "", err
	}
	if r != nil {
		r.Invalidate(a.Name)
	}
	return fmt.Sprintf("transform %s %s OK", a.Name, a.Op), nil
}

func Group(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Name  string   `json:"name"`
		Items []string `json:"items"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	if err := w.Group(a.Name, a.Items); err != nil {
		return "", err
	}
	if r != nil {
		r.Invalidate(a.Name)
	}
	return fmt.Sprintf("grouped %s from %v OK", a.Name, a.Items), nil
}

func ExportSTL(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Name                string `json:"name"`
		Filename            string `json:"filename"`
		ResolutionDivisions int    `json:"resolution_divisions"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	if a.ResolutionDivisions == 0 {
		a.ResolutionDivisions = 512
	}
	s, _, ok := w.Get(a.Name)
	if !ok {
		return "", fmt.Errorf("object %q not found", a.Name)
	}
	cfg := simplesdf.STLConfig{
		ResolutionDivisions: uint(a.ResolutionDivisions),
		UseGPU:              false, // CPU safe default; TUI can opt-in later
	}
	start := time.Now()
	if err := s.SaveSTL(a.Filename, cfg); err != nil {
		return "", fmt.Errorf("stl export: %w", err)
	}
	dur := time.Since(start)
	return fmt.Sprintf("exported %s -> %s (divs=%d) in %v", a.Name, filepath.Base(a.Filename), a.ResolutionDivisions, dur.Round(time.Millisecond)), nil
}

// Export3MF writes a named object to a 3MF file (mesh, millimeters).
func Export3MF(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Name                string `json:"name"`
		Filename            string `json:"filename"`
		ResolutionDivisions int    `json:"resolution_divisions"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	if a.ResolutionDivisions == 0 {
		a.ResolutionDivisions = 512
	}
	s, _, ok := w.Get(a.Name)
	if !ok {
		return "", fmt.Errorf("object %q not found", a.Name)
	}
	f, err := os.Create(a.Filename)
	if err != nil {
		return "", fmt.Errorf("3mf export: %w", err)
	}
	defer f.Close()
	start := time.Now()
	if err := render.WriteSDF3MF(f, a.Name, s, a.ResolutionDivisions); err != nil {
		return "", fmt.Errorf("3mf export: %w", err)
	}
	dur := time.Since(start)
	return fmt.Sprintf("exported %s -> %s (divs=%d) in %v", a.Name, filepath.Base(a.Filename), a.ResolutionDivisions, dur.Round(time.Millisecond)), nil
}

func RenderPreview(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Name       string `json:"name"`
		Projection string `json:"projection"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	s, _, ok := w.Get(a.Name)
	if !ok {
		return "", fmt.Errorf("object %q not found", a.Name)
	}
	proj := render.Projection(strings.ToLower(a.Projection))
	if proj != render.ProjXY && proj != render.ProjXZ && proj != render.ProjYZ {
		proj = render.ProjXY
	}
	maxW, maxH := a.Width, a.Height
	if maxW <= 0 {
		maxW = 256
	}
	if maxH <= 0 {
		maxH = 160
	}

	img, rect, err := r.Render(s, a.Name, proj, maxW, maxH)
	if err != nil {
		w.SetPreview(a.Name, world.Projection(proj), rect.Dx(), rect.Dy(), false, err.Error())
		return "", fmt.Errorf("preview render failed: %w", err)
	}
	_ = img // caller (TUI) will pull via world.GetPreview + shared renderer if needed
	w.SetPreview(a.Name, world.Projection(proj), rect.Dx(), rect.Dy(), true, "")
	return fmt.Sprintf("rendered preview %s (%s) %dx%d OK", a.Name, proj, rect.Dx(), rect.Dy()), nil
}

func DescribeState(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return w.Describe(), nil
}

func GetBoundingBox(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	bb, ok := w.Bounds(a.Name)
	if !ok {
		return "", fmt.Errorf("object not found")
	}
	return fmt.Sprintf("bbox %s: min=(%.4f,%.4f,%.4f) max=(%.4f,%.4f,%.4f) size=(%.4f,%.4f,%.4f)",
		a.Name, bb.Min.X, bb.Min.Y, bb.Min.Z, bb.Max.X, bb.Max.Y, bb.Max.Z,
		bb.Max.X-bb.Min.X, bb.Max.Y-bb.Min.Y, bb.Max.Z-bb.Min.Z), nil
}

func SaveWorld(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	if !strings.HasSuffix(a.Filename, ".cad.json") {
		a.Filename += ".cad.json"
	}
	if err := w.Save(a.Filename); err != nil {
		return "", err
	}
	return fmt.Sprintf("saved history to %s (%d ops)", a.Filename, len(w.History())), nil
}

func LoadWorld(ctx context.Context, w *world.World, r *render.Renderer, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	if err := w.Load(a.Filename); err != nil {
		return "", err
	}
	if r != nil {
		r.ClearCache()
	}
	return fmt.Sprintf("loaded %s (%d objects now)", a.Filename, len(w.Names())), nil
}
