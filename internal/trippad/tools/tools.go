// Package tools exposes shader editing, parameter tuning and image previews.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/wgslls"
)

const SystemPrompt = `You create animated psychedelic WGSL shaders in trippad.
Decide whether the user's request is a new design or an edit of the current design.
For a new subject/scene, use mode="new" in validation and the first trip_set_shader. Start from a neutral shade_body skeleton: return vec3<f32>(0.0); then build the requested design with uv and uni.time. Do not copy the starter's effects, name or controls. Supply a fresh descriptive name and a complete params array (use [] for no controls). The current animation is only a fallback and stays visible until your replacement compiles; you do not need trip_describe to start a new design.
For an edit, continuation, or refinement, use mode="edit" and trip_describe to inspect the live source and values. Once your new design is installed, refinements use mode="edit". If the request is ambiguous, preserve the current design as an edit. Do not reuse an earlier request's plan for a new subject.
Use trip_validate_shader to test proposed shade_body and params without changing the live shader. Diagnostics identify shade_body lines separately from generated full_wgsl or backend code. compile_ok reports the actual renderer compiler; lsp_status says whether language-server analysis completed. Do not treat unavailable/timed-out analysis as clean. When available, trip_complete_shader provides completion suggestions for current or proposed source; positions use 1-based shade_body lines and UTF-8 byte columns. Validate substantial edits before trip_set_shader.
trip_set_shader accepts ONLY the body of fn shade(uv: vec2<f32>) -> vec3<f32>.
Do not include fn declarations, bindings or a complete module: nested functions are invalid WGSL.
Return RGB in [0,1]. uv is centered, aspect corrected, with y increasing downward,
vertical range -1..1. Uniforms: uni.time (seconds), uni.dt, uni.frame, uni.w, uni.h.
Helpers: hash(vec2)->f32, noise(vec2)->f32, fbm(vec2)->f32,
palette(f32)->vec3 (cosine rainbow), rot2d(f32)->mat2x2.
Named parameters are accessed through p_NAME() -> f32, maximum 16.
Every proposed shade_body must declare mode="new" or "edit". New mode requires name and explicit params; edit mode can omit name/params to preserve them. parameter_usage.unused flags controls not referenced by the body; use or remove those controls. A reference check does not prove visual effectiveness.
trip_set_shader reports status, changed, individual changes, name, revision, controls and values. status="unchanged" means no compile, revision increment or gallery write occurred; do not claim a rewrite or repeat the same submission. changes.body=false means only metadata or controls changed. Validation body_matches_live=true likewise means the body is not a rewrite.
Parameter definitions require name, min, max, step, default; names use ASCII
letters, digits and underscores and start with a letter. In edit mode, omit params to keep
the existing controls and values; supply [] to remove them. Use WGSL syntax,
not GLSL. Compilation diagnostics are returned (long errors may be excerpted); correct them and retry.
Keep loops bounded and shaders fast enough for live animation.
Use trip_preview to inspect the actual frame only when that tool is available.
Use descriptive names for new shaders so users can find them in the gallery.
Successful shader replacements are automatically archived by the app.
Use trip_save_preset when asked to export a named file. A preset includes shader and controls.`

func Schemas() []ds4.ToolSchema {
	spec := func(name, desc, schema string) ds4.ToolSchema {
		return ds4.ToolSchema{Name: name, Description: desc, Parameters: json.RawMessage(schema)}
	}
	empty := `{"type":"object","properties":{}}`
	file := `{"type":"object","properties":{"filename":{"type":"string","minLength":1}},"required":["filename"]}`
	return []ds4.ToolSchema{
		spec("trip_validate_shader", "Compile-check and analyze current or proposed shader without changing live state or saving a gallery version. Omitted shade_body/params use current values. Proposed shade_body requires mode new/edit; new also requires name and explicit params. Reports compiler result, LSP status, unchanged body and unused controls.", `{"type":"object","properties":{"shade_body":{"type":"string"},"params":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"name":{"type":"string"},"min":{"type":"number"},"max":{"type":"number"},"step":{"type":"number"},"default":{"type":"number"}},"required":["name","min","max","step","default"]}},"mode":{"type":"string","enum":["new","edit"],"description":"new design or edit of the live design; required with shade_body"},"name":{"type":"string","minLength":1,"description":"fresh descriptive name required for new designs; omit to preserve in edit mode"}}}`),
		spec("trip_complete_shader", "Get WGSL completions for current or proposed shader. line/character are 1-based shade_body positions; character counts UTF-8 bytes. Proposed shade_body requires mode new/edit; new requires name and params. No live changes.", `{"type":"object","properties":{"line":{"type":"integer","minimum":1},"character":{"type":"integer","minimum":1},"shade_body":{"type":"string"},"params":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"name":{"type":"string"},"min":{"type":"number"},"max":{"type":"number"},"step":{"type":"number"},"default":{"type":"number"}},"required":["name","min","max","step","default"]}},"mode":{"type":"string","enum":["new","edit"],"description":"new design or edit of the live design; required with shade_body"},"name":{"type":"string","minLength":1,"description":"fresh descriptive name required for new designs; omit to preserve in edit mode"}},"required":["line","character"]}`),
		spec("trip_set_shader", "Apply a new design or edit. New requires a fresh name and explicit params; edit preserves omitted metadata. Returns changed/no-op status and unused controls. Failed compilation preserves live state.", `{"type":"object","properties":{"name":{"type":"string","minLength":1,"description":"fresh descriptive name required for new designs; omit to preserve in edit mode"},"shade_body":{"type":"string"},"params":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"name":{"type":"string"},"min":{"type":"number"},"max":{"type":"number"},"step":{"type":"number"},"default":{"type":"number"}},"required":["name","min","max","step","default"]}},"mode":{"type":"string","enum":["new","edit"],"description":"new design or edit of the live design; required with shade_body"}},"required":["mode","shade_body"]}`),
		spec("trip_list_params", "List definitions, ranges and current values.", empty),
		spec("trip_set_param", "Set a named float parameter, clamped to its range.", `{"type":"object","properties":{"name":{"type":"string"},"value":{"type":"number"}},"required":["name","value"]}`),
		spec("trip_randomize", "Randomize all parameter values within their ranges.", empty),
		spec("trip_preview", "Render the current frame as a PNG image for visual inspection.", empty),
		spec("trip_describe", "Describe current shader source, controls, time and FPS.", empty),
		spec("trip_save_preset", "Save shader, definitions and values to .trip.json.", file),
		spec("trip_load_preset", "Compile and load a .trip.json preset.", file),
	}
}

type Handler func(context.Context, *State, json.RawMessage) (ds4.ToolResult, error)

func textResult(s string) ds4.ToolResult { return ds4.ToolResult{Parts: []ds4.ContentPart{{Text: s}}} }
func jsonResult(v any) (ds4.ToolResult, error) {
	b, e := json.Marshal(v)
	return textResult(string(b)), e
}

func Registry(servers ...*wgslls.Service) map[string]Handler {
	var server *wgslls.Service
	if len(servers) > 0 {
		server = servers[0]
	}
	return map[string]Handler{
		"trip_validate_shader": validationHandler(server),
		"trip_complete_shader": completionHandler(server),
		"trip_set_shader":      setShader,
		"trip_list_params": func(_ context.Context, s *State, _ json.RawMessage) (ds4.ToolResult, error) {
			snap := s.Snapshot()
			return jsonResult(struct {
				Params []params.Param     `json:"params"`
				Values map[string]float32 `json:"values"`
			}{snap.Source.Params, snap.Named})
		},
		"trip_set_param": func(_ context.Context, s *State, raw json.RawMessage) (ds4.ToolResult, error) {
			var a struct {
				Name  string   `json:"name"`
				Value *float32 `json:"value"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return ds4.ToolResult{}, err
			}
			if a.Value == nil {
				return ds4.ToolResult{}, fmt.Errorf("value required")
			}
			v, err := s.Set(a.Name, *a.Value)
			return textResult(fmt.Sprintf("%s = %g", a.Name, v)), err
		},
		"trip_randomize": func(_ context.Context, s *State, _ json.RawMessage) (ds4.ToolResult, error) {
			s.Randomize()
			return jsonResult(s.Snapshot().Named)
		},
		"trip_preview": func(ctx context.Context, s *State, _ json.RawMessage) (ds4.ToolResult, error) {
			snap := s.Snapshot()
			img, err := s.Render(snap, snap.Width, snap.Height)
			if err != nil {
				return ds4.ToolResult{}, err
			}
			if err = ctx.Err(); err != nil {
				return ds4.ToolResult{}, err
			}
			png, err := ds4.ImageInputPNG(img)
			if err != nil {
				return ds4.ToolResult{}, err
			}
			return ds4.ToolResult{Parts: []ds4.ContentPart{{Text: fmt.Sprintf("%s at %.2fs", snap.Source.Name, snap.Time)}, {Image: &png}}}, nil
		},
		"trip_describe": func(_ context.Context, s *State, _ json.RawMessage) (ds4.ToolResult, error) {
			return jsonResult(s.Snapshot())
		},
		"trip_save_preset": fileHandler(false), "trip_load_preset": fileHandler(true),
	}
}
func setShader(_ context.Context, s *State, raw json.RawMessage) (ds4.ToolResult, error) {
	snap := s.Snapshot()
	src, values, _, err := candidate(snap, raw, true)
	if err != nil {
		return ds4.ToolResult{}, err
	}
	after, changes, err := s.replace(src, values, snap.Revision)
	if err != nil {
		d := compileDiagnostic(src, err)
		hint := strings.Join(shaderHints(src.ShadeBody), "\n")
		return ds4.ToolResult{}, fmt.Errorf("%s:%d:%d: %s\n%s", d.Location, d.Line, d.Column, d.Message, hint)
	}
	usage := inspectParams(after.Source)
	status := "applied"
	hints := append(shaderHints(after.Source.ShadeBody), usage.hints()...)
	if !changes.any() {
		status = "unchanged"
		hints = append(hints, "Identical shader, name, parameter definitions and values. Nothing was compiled, changed or archived. Do not count this as a rewrite.")
	} else if !changes.Body {
		hints = append(hints, "shade_body did not change; only the reported metadata/control changes were applied.")
	}
	return jsonResult(struct {
		Status         string             `json:"status"`
		Changed        bool               `json:"changed"`
		CompileChecked bool               `json:"compile_checked"`
		Changes        shaderChanges      `json:"changes"`
		Name           string             `json:"name"`
		Revision       uint64             `json:"revision"`
		Params         []params.Param     `json:"params"`
		Values         map[string]float32 `json:"values"`
		Usage          parameterUsage     `json:"parameter_usage"`
		Hints          []string           `json:"hints,omitempty"`
	}{status, changes.any(), changes.any(), changes, after.Source.Name, after.Revision, append([]params.Param{}, after.Source.Params...), after.Named, usage, hints})
}
func fileHandler(load bool) Handler {
	return func(_ context.Context, s *State, raw json.RawMessage) (ds4.ToolResult, error) {
		var a struct {
			Filename string `json:"filename"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return ds4.ToolResult{}, err
		}
		if strings.TrimSpace(a.Filename) == "" {
			return ds4.ToolResult{}, fmt.Errorf("filename required")
		}
		if load {
			return textResult("loaded " + params.Filename(a.Filename)), s.Load(a.Filename)
		}
		return textResult("saved " + params.Filename(a.Filename)), s.Save(a.Filename)
	}
}
func Register(s *State, vision bool, servers ...*wgslls.Service) (*ds4.ToolRegistry, error) {
	reg := ds4.NewToolRegistry()
	handlers := Registry(servers...)
	for _, schema := range Schemas() {
		if schema.Name == "trip_complete_shader" && (len(servers) == 0 || servers[0] == nil || servers[0].Command == "") {
			continue
		}
		if schema.Name == "trip_preview" && !vision {
			continue
		}
		fn := handlers[schema.Name]
		err := reg.Register(ds4.MultimodalTool{ToolSchema: schema, Handler: func(ctx context.Context, raw json.RawMessage) (ds4.ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return ds4.ToolResult{}, err
			}
			result, err := fn(ctx, s, raw)
			if err != nil {
				if ctx.Err() != nil {
					return ds4.ToolResult{}, ctx.Err()
				}
				// ToolRegistry aborts the run on handler errors. Return operational
				// errors as observations so the model can correct shader mistakes.
				return textResult("ERROR: " + err.Error()), nil
			}
			return result, nil
		}})
		if err != nil {
			return nil, err
		}
	}
	return reg, nil
}
