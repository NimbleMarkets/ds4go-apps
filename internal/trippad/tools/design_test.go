package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
)

func toolCall(t *testing.T, reg *ds4.ToolRegistry, name, args string) string {
	t.Helper()
	out, err := reg.ExecuteToolCalls(context.Background(), []ds4.ToolCall{{ID: "test", Name: name, Arguments: args}})
	if err != nil {
		t.Fatal(err)
	}
	return out[0].Content
}

func TestNewDesignRequiresFreshIdentityAndExplicitControls(t *testing.T) {
	s, r := testState(t)
	s.Set("speed", 2)
	before := s.Snapshot()
	archives := 0
	s.SetArchive(func(params.Preset) error { archives++; return nil })
	reg, _ := Register(s, false)
	for _, raw := range []string{
		`{"shade_body":"return vec3<f32>(0.0);"}`,
		`{"mode":"surprise","shade_body":"return vec3<f32>(0.0);"}`,
		`{"mode":"new","shade_body":"return vec3<f32>(0.0);","params":[]}`,
		`{"mode":"new","name":"submarine","shade_body":"return vec3<f32>(0.0);"}`,
		`{"mode":"new","name":"submarine","shade_body":"return vec3<f32>(0.0);","params":null}`,
		`{"mode":"new","name":" Plasma ","shade_body":"return vec3<f32>(0.0);","params":[]}`,
	} {
		for _, name := range []string{"trip_set_shader", "trip_validate_shader"} {
			if got := toolCall(t, reg, name, raw); !strings.HasPrefix(got, "ERROR:") {
				t.Fatalf("accepted %s: %s", raw, got)
			}
		}
	}
	if r.compiles != 0 || archives != 0 || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("invalid design touched live state")
	}
	raw := `{"mode":"new","name":"yellow submarine","shade_body":"return vec3<f32>(p_bubbles());","params":[{"name":"bubbles","min":0,"max":2,"step":0.1,"default":0.5}]}`
	if got := toolCall(t, reg, "trip_validate_shader", raw); !strings.Contains(got, `"compile_ok":true`) {
		t.Fatal(got)
	}
	if archives != 0 || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("validation replaced display fallback")
	}
	r.err = errors.New("test compile failure")
	if got := toolCall(t, reg, "trip_set_shader", raw); !strings.Contains(got, "test compile failure") {
		t.Fatal(got)
	}
	if archives != 0 || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("failed new design replaced display fallback")
	}
	r.err = nil
	got := toolCall(t, reg, "trip_set_shader", raw)
	after := s.Snapshot()
	if !strings.Contains(got, `"status":"applied"`) || after.Source.Name != "yellow submarine" || len(after.Source.Params) != 1 || after.Named["bubbles"] != 0.5 || archives != 2 {
		t.Fatalf("new design inherited starter metadata: %s %+v", got, after)
	}
	// Refinements preserve the new design's tuned controls, not starter values.
	s.Set("bubbles", 1.4)
	got = toolCall(t, reg, "trip_set_shader", `{"mode":"edit","shade_body":"return vec3<f32>(p_bubbles() * uv.x);"}`)
	if s.Snapshot().Named["bubbles"] != 1.4 || s.Snapshot().Source.Name != "yellow submarine" {
		t.Fatal(got)
	}
	// A new design with [] intentionally has no inherited controls.
	got = toolCall(t, reg, "trip_set_shader", `{"mode":"new","name":"empty controls","shade_body":"return vec3<f32>(uv.x);","params":[]}`)
	if strings.HasPrefix(got, "ERROR:") || len(s.Snapshot().Named) != 0 {
		t.Fatal(got)
	}
}

func TestIdenticalSubmissionDoesNotCompileArchiveOrAdvanceRevision(t *testing.T) {
	s, r := testState(t)
	s.Set("speed", 2)
	before := s.Snapshot()
	archives := 0
	s.SetArchive(func(params.Preset) error { archives++; return nil })
	reg, _ := Register(s, false)
	raw, _ := json.Marshal(map[string]any{"mode": "edit", "shade_body": before.Source.ShadeBody})
	got := toolCall(t, reg, "trip_set_shader", string(raw))
	if !strings.Contains(got, `"status":"unchanged"`) || !strings.Contains(got, `"changed":false`) || !strings.Contains(got, `"compile_checked":false`) {
		t.Fatal(got)
	}
	if r.compiles != 0 || archives != 0 || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("no-op mutated state or did expensive work")
	}
	// Re-supplying definitions resets tuned values, so it is a real change even
	// with identical source. Report it as a value change, not a body rewrite.
	raw, _ = json.Marshal(map[string]any{"mode": "edit", "shade_body": before.Source.ShadeBody, "params": before.Source.Params})
	got = toolCall(t, reg, "trip_set_shader", string(raw))
	var result struct {
		Changed  bool
		Changes  shaderChanges
		Revision uint64
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatal(err, got)
	}
	if !result.Changed || result.Changes.Body || result.Changes.Params || !result.Changes.Values || result.Revision != before.Revision+1 || r.compiles != 1 || archives != 2 {
		t.Fatal(got)
	}
	// Check stale revisions even for a would-be no-op.
	if err := s.Replace(s.Snapshot().Source, nil, before.Revision); err == nil {
		t.Fatal("stale no-op accepted")
	}
}

func TestUnusedParameterWarningsIgnoreCommentsAndSimilarNames(t *testing.T) {
	defs := []params.Param{
		{Name: "scale", Min: 0, Max: 2, Step: .1, Default: 1},
		{Name: "scale_extra", Min: 0, Max: 2, Step: .1, Default: 1},
		{Name: "speed", Min: 0, Max: 2, Step: .1, Default: 1},
	}
	body := "// p_scale()\n/* nested /* p_scale() */ comment */\nlet p_scale = 1.0;\nreturn vec3<f32>(p_scale_extra() + p_speed /* comment */ ());"
	u := inspectParams(shader.Source{ShadeBody: body, Params: defs})
	if !u.Checked || !reflect.DeepEqual(u.Unused, []string{"scale"}) || !reflect.DeepEqual(u.Referenced, []string{"scale_extra", "speed"}) {
		t.Fatalf("bad usage: %+v", u)
	}
	s, _ := testState(t)
	reg, _ := Register(s, false)
	raw, _ := json.Marshal(map[string]any{"mode": "new", "name": "submarine", "shade_body": body, "params": defs})
	for _, name := range []string{"trip_validate_shader", "trip_set_shader"} {
		got := toolCall(t, reg, name, string(raw))
		if !strings.Contains(got, `"unused":["scale"]`) || !strings.Contains(got, "Unused controls: scale") {
			t.Fatal(name, got)
		}
	}
	got := toolCall(t, reg, "trip_validate_shader", `{}`)
	if !strings.Contains(got, `"body_matches_live":true`) {
		t.Fatal(got)
	}
}

type blockedRenderer struct {
	fakeRenderer
	started, release chan struct{}
}

func (r *blockedRenderer) Compile(shader.Source) error { close(r.started); <-r.release; return nil }

func TestNewShaderCannotOverwriteConcurrentControlChange(t *testing.T) {
	r := &blockedRenderer{started: make(chan struct{}), release: make(chan struct{})}
	s, err := NewState(shader.Starters()[0], r)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	done := make(chan error, 1)
	go func() { done <- s.Replace(shader.Starters()[1], nil, before.Revision) }()
	<-r.started
	s.Set("speed", 2)
	close(r.release)
	if err := <-done; err == nil {
		t.Fatal("stale compile overwrote control change")
	}
	if after := s.Snapshot(); after.Source.Name != before.Source.Name || after.Named["speed"] != 2 {
		t.Fatal("lost live state")
	}
}
