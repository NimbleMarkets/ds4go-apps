package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readGLSLGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "glsl", name+".glsl"))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return string(b)
}

func TestTranspileSphere(t *testing.T) {
	glsl := readGLSLGolden(t, "sphere")
	wgsl, err := transpileGLSLToWGSL(glsl)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(wgsl, "fn sdf(p: vec3<f32>) -> f32") {
		t.Errorf("missing sdf signature:\n%s", wgsl)
	}
	if !strings.Contains(wgsl, "length(p)") {
		t.Errorf("sphere body lost:\n%s", wgsl)
	}
}

func TestNormalizeFloatLiterals(t *testing.T) {
	cases := []struct{ in, want string }{
		{"length(p)-3.;", "length(p)-3.0;"},
		{".5", "0.5"},
		{"3.", "3.0"},
		{"0.0", "0.0"},
		{"1.5", "1.5"},
		{"vec3(1.,2.,3.)", "vec3(1.0,2.0,3.0)"},
		{"-2.0*h", "-2.0*h"},
		{"clamp( p.x, -2.0*h, 0.0 )", "clamp( p.x, -2.0*h, 0.0 )"},
		{"h/k", "h/k"},
		{"p.x", "p.x"}, // member access must not become a float
	}
	for _, c := range cases {
		got := normalizeFloatLiterals(c.in)
		if got != c.want {
			t.Errorf("normalizeFloatLiterals(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
