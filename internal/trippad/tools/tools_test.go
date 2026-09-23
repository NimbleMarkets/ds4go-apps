package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
)

type fakeRenderer struct {
	err      error
	compiles int
}

func (f *fakeRenderer) Compile(shader.Source) error { f.compiles++; return f.err }
func (f *fakeRenderer) Render(_ shader.Source, _ [16]float32, _, _ float32, _ uint32, w, h int) (*image.NRGBA, error) {
	return image.NewNRGBA(image.Rect(0, 0, w, h)), nil
}
func testState(t *testing.T) (*State, *fakeRenderer) {
	t.Helper()
	r := &fakeRenderer{}
	s, err := NewState(shader.Starters()[0], r)
	if err != nil {
		t.Fatal(err)
	}
	return s, r
}
func TestToolsAndCompileFeedback(t *testing.T) {
	s, r := testState(t)
	reg, err := Register(s, true)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name, args string) ds4.ChatMessage {
		t.Helper()
		res, err := reg.ExecuteToolCalls(context.Background(), []ds4.ToolCall{{ID: "1", Name: name, Arguments: args}})
		if err != nil {
			t.Fatal(err)
		}
		return res[0]
	}
	call("trip_set_param", `{"name":"speed","value":999}`)
	if s.Snapshot().Named["speed"] != 3 {
		t.Fatal("not clamped")
	}
	before := s.Snapshot()
	r.err = errors.New("naga: line 37: unknown identifier oops")
	res := call("trip_set_shader", `{"mode":"edit","shade_body":"return oops;"}`)
	if !strings.Contains(res.Content, r.err.Error()) {
		t.Fatalf("lost compiler error: %s", res.Content)
	}
	if s.Snapshot().Source.ShadeBody != before.Source.ShadeBody {
		t.Fatal("failed compile changed shader")
	}
	r.err = nil
	res = call("trip_set_shader", `{"mode":"edit","shade_body":"return palette(uv.x);"}`)
	if !strings.Contains(res.Content, `"status":"applied"`) || s.Snapshot().Named["speed"] != 3 {
		t.Fatal("edit lost controls")
	}
	s.Viewport(64, 40)
	res = call("trip_preview", `{}`)
	if len(res.Parts) != 2 || res.Parts[1].Image == nil {
		t.Fatalf("missing image: %+v", res)
	}
	img, err := png.Decode(bytes.NewReader(res.Parts[1].Image.Data))
	if err != nil || img.Bounds().Dx() != 64 || img.Bounds().Dy() != 40 {
		t.Fatal("invalid PNG", err)
	}
	textReg, _ := Register(s, false)
	for _, schema := range textReg.Schemas() {
		if schema.Name == "trip_preview" {
			t.Fatal("text model has preview")
		}
	}
	for _, schema := range Schemas() {
		if !json.Valid(schema.Parameters) || Registry()[schema.Name] == nil {
			t.Fatal("invalid schema/handler", schema.Name)
		}
	}
	path := filepath.Join(t.TempDir(), "roundtrip")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	s.Set("speed", 0)
	if err := s.Load(path); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Named["speed"] != 3 {
		t.Fatal("preset lost values")
	}
	if err := s.Replace(before.Source, nil, 0); err == nil {
		t.Fatal("stale edit committed")
	}
}
func TestConcurrentSnapshotsAndControls(t *testing.T) {
	s, _ := testState(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			for j := 0; j < 100; j++ {
				s.Set("speed", float32(j))
				snap := s.Snapshot()
				s.Render(snap, 8, 6)
				s.Clock(float32(j), .03, uint32(j), 30)
			}
		})
	}
	wg.Wait()
}
