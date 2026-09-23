package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
)

func TestValidationDoesNotMutateOrArchive(t *testing.T) {
	s, _ := testState(t)
	before := s.Snapshot()
	archives := 0
	s.SetArchive(func(params.Preset) error { archives++; return nil })
	reg, err := Register(s, false)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"mode":"edit","shade_body":"return vec3<f32>(0.5);","params":[]}`
	out, err := reg.ExecuteToolCalls(context.Background(), []ds4.ToolCall{{ID: "check", Name: "trip_validate_shader", Arguments: raw}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		CompileOK bool   `json:"compile_ok"`
		LSPStatus string `json:"lsp_status"`
	}
	if err := json.Unmarshal([]byte(out[0].Content), &result); err != nil {
		t.Fatal(out[0].Content, err)
	}
	if !result.CompileOK || !strings.Contains(result.LSPStatus, "unavailable") {
		t.Fatalf("%+v", result)
	}
	if archives != 0 || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("validation changed live shader, parameters, revision or gallery")
	}
	out, err = reg.ExecuteToolCalls(context.Background(), []ds4.ToolCall{{ID: "empty", Name: "trip_validate_shader", Arguments: `{"mode":"edit","shade_body":""}`}})
	if err != nil || !strings.Contains(out[0].Content, "shade body required") {
		t.Fatalf("empty candidate silently accepted: %+v %v", out, err)
	}
}

func TestCompileDiagnosticMapsBodyAndRetainsBackendCoordinates(t *testing.T) {
	src := shader.Starters()[0]
	doc, err := shader.BuildDocument(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{fmt.Sprintf("WGSL: line %d, column 7: unknown name", doc.BodyStart+1), fmt.Sprintf("WGSL: %d:7: unknown name", doc.BodyStart+1)} {
		d := compileDiagnostic(src, errors.New(message))
		if d.Location != "shade_body" || d.Line != 2 || d.Column != 7 {
			t.Fatalf("bad mapping: %+v", d)
		}
	}
	d := compileDiagnostic(src, errors.New("failed to compile MSL: program_source:45:6: error"))
	if d.Location != "backend" || d.Line != 0 {
		t.Fatalf("translated MSL misreported as WGSL: %+v", d)
	}
	if len(shaderHints("fn nested() {}")) != 1 || len(shaderHints("let a = atan2(uv.y,uv.x);")) != 1 {
		t.Fatal("missing targeted hints")
	}
}
