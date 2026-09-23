package wgslls

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/gogpu/naga"
)

func installedServer(t *testing.T) string {
	t.Helper()
	command := os.Getenv("TRIPPAD_TEST_WGSL_LSP")
	if command == "" {
		command = Find("auto")
	}
	if command == "" {
		p, _ := filepath.Abs("../../../bin/wgsl-analyzer")
		if _, err := os.Stat(p); err == nil {
			command = p
		}
	}
	if command == "" {
		t.Skip("wgsl-analyzer not installed")
	}
	return command
}

func TestServer(t *testing.T) {
	command := installedServer(t)
	s := New(command)
	defer s.Close()
	ctx := context.Background()
	for _, body := range []string{"return vec3<f32>(1.0);", "let bad = ;\nreturn vec3<f32>(1.0);", "return vec3<f32>(1.0);", "let color: f32 = vec3<f32>(1.0);\nreturn vec3<f32>(color);"} {
		src := shader.Source{Name: "test", ShadeBody: body}
		doc, err := shader.BuildDocument(src)
		if err != nil {
			t.Fatal(err)
		}
		ds, err := s.Check(ctx, doc)
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		errors := 0
		for _, d := range ds {
			if d.Severity == "error" {
				errors++
				if d.Location != "shade_body" {
					t.Fatalf("wrapper diagnostic: %+v", d)
				}
			}
		}
		// Rechecking an identical candidate must reuse completed diagnostics,
		// including an empty publication; the upstream does not republish on demand.
		again, err := s.Check(ctx, doc)
		if err != nil || !reflect.DeepEqual(ds, again) {
			t.Fatalf("repeat: %v %v", again, err)
		}
		bad := strings.Contains(body, "let ")
		if (errors > 0) != bad {
			t.Fatalf("body=%q diagnostics=%+v", body, ds)
		}
	}
	for _, src := range shader.Starters() {
		doc, err := shader.BuildDocument(src)
		if err != nil {
			t.Fatal(err)
		}
		ds, err := s.Check(ctx, doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range ds {
			if d.Severity == "error" {
				t.Errorf("starter %s: %+v", src.Name, d)
			}
		}
	}
	completion, err := s.Complete(ctx, shader.Source{ShadeBody: "return vec3<f32>(uni.);"}, 1, 22)
	if err != nil || !strings.Contains(completion, "time") {
		t.Fatalf("completion=%q err=%v", completion, err)
	}
	prefix := "/* 🌈 */ return vec3<f32>(uni."
	completion, err = s.Complete(ctx, shader.Source{ShadeBody: prefix + ");"}, 1, len(prefix)+1)
	if err != nil || !strings.Contains(completion, "time") {
		t.Fatalf("Unicode completion=%q err=%v", completion, err)
	}
	if _, err := s.Complete(ctx, shader.Source{ShadeBody: "return vec3<f32>(1.0);"}, 99, 1); err == nil {
		t.Fatal("invalid body position accepted")
	}

}

// Upstream v0.9.11 swallowed the minus in uv.x-0.1 into a numeric token,
// producing false smoothstep/mix arity errors for compiler-valid shaders.
func TestSubtractionDiagnostics(t *testing.T) {
	s := New(installedServer(t))
	defer s.Close()
	for _, expression := range []string{
		"smoothstep(0.0, 1.0, uv.x-0.1)",
		"smoothstep(0.0, 1.0, uv.x - 0.1)",
		"mix(vec3<f32>(0.0), vec3<f32>(1.0), smoothstep(0.0, 1.0, uv.x-0.1))",
		"-0.1", "-1.0-2.0", "uv.x- -0.1", "(uv.x)-0.1f",
		"sin(uv.x)-1e-3f", "f32(2i-1i)",
		"f32(2u-1u)", "f32(0x2i-0x1i)", "f32(-2147483647i)",
	} {
		t.Run(expression, func(t *testing.T) {
			doc, err := shader.BuildDocument(shader.Source{ShadeBody: "return vec3<f32>(" + expression + ");"})
			if err != nil {
				t.Fatal(err)
			}
			ast, err := naga.Parse(doc.Code)
			if err != nil {
				t.Fatal(err)
			}
			mod, err := naga.Lower(ast)
			if err != nil {
				t.Fatal(err)
			}
			if errs, err := naga.Validate(mod); err != nil || len(errs) != 0 {
				t.Fatalf("compiler validation: %v %v", err, errs)
			}
			diagnostics, err := s.Check(context.Background(), doc)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range diagnostics {
				if d.Severity == "error" {
					t.Errorf("false diagnostic for compiler-valid expression: %+v", d)
				}
			}
		})
	}
}

func TestRejectBrokenUpstream(t *testing.T) {
	command := os.Getenv("TRIPPAD_TEST_BROKEN_WGSL_LSP")
	if command == "" {
		t.Skip("optional incompatible-server check")
	}
	s := New(command)
	defer s.Close()
	doc, _ := shader.BuildDocument(shader.Source{ShadeBody: "return vec3<f32>(1.0);"})
	if _, err := s.Check(context.Background(), doc); err == nil {
		t.Fatal("incompatible server accepted")
	}
	start := time.Now()
	if _, err := s.Check(context.Background(), doc); err == nil || time.Since(start) > time.Second {
		t.Fatalf("failure not retained: %v", err)
	}
}
