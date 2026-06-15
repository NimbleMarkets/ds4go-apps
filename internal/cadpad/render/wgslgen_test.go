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

// TestTranspileRejectsUnknownIdent verifies the allowlist is real: a GLSL
// builtin we do NOT support ('texture') must trigger the error path so the
// caller can fall back to the CPU renderer.
func TestTranspileRejectsUnknownIdent(t *testing.T) {
	_, err := transpileGLSLToWGSL("float f(vec3 p){\nreturn texture(p)-1.0;\n}\n")
	if err == nil {
		t.Fatal("expected error for unsupported identifier 'texture', got nil")
	}
}

// TestTranspileCorpusNoError locks in "no regression": every golden that
// transpiles today must continue to transpile after the allowlist change. All
// eleven captured goldens (including triprism, whose braceless `if` is a
// syntactic concern for Task 1.3 but contains no unknown identifiers) currently
// pass the transpiler end-to-end, so none are excluded here.
func TestTranspileCorpusNoError(t *testing.T) {
	names := []string{
		"sphere", "box", "box_round", "cylinder", "torus", "hexprism",
		"triprism", "boxframe", "union", "diff", "translate",
	}
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join("testdata", "glsl", name+".glsl"))
		if err != nil {
			t.Fatalf("read golden %s: %v", name, err)
		}
		if _, err := transpileGLSLToWGSL(string(b)); err != nil {
			t.Errorf("transpile %s: unexpected error: %v", name, err)
		}
	}
}

// corpusNames is the full set of captured goldens, used by the snapshot and
// compile tests.
var corpusNames = []string{
	"sphere", "box", "box_round", "cylinder", "torus", "hexprism",
	"triprism", "boxframe", "union", "diff", "translate",
}

// TestTranspileCorpus is a snapshot test: under UPDATE_GOLDEN it writes the
// transpiled WGSL to testdata/wgsl/<name>.wgsl; otherwise it compares against the
// captured snapshot. Snapshots prove "no regression" — they do NOT prove the
// WGSL is valid; TestTranspileCorpusCompiles is the real validity gate.
func TestTranspileCorpus(t *testing.T) {
	dir := filepath.Join("testdata", "wgsl")
	for _, name := range corpusNames {
		glsl := readGLSLGolden(t, name)
		wgsl, err := transpileGLSLToWGSL(glsl)
		if err != nil {
			t.Fatalf("transpile %s: %v", name, err)
		}
		golden := filepath.Join(dir, name+".wgsl")
		if os.Getenv("UPDATE_GOLDEN") != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("%s: mkdir: %v", name, err)
			}
			if err := os.WriteFile(golden, []byte(wgsl), 0o644); err != nil {
				t.Fatalf("%s: write golden: %v", name, err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%s: read wgsl golden (run with UPDATE_GOLDEN=1 first): %v", name, err)
		}
		if string(want) != wgsl {
			t.Errorf("%s WGSL changed:\n--- got ---\n%s", name, wgsl)
		}
	}
}

// TestTranspileCorpusCompiles is the correctness gate: it transpiles each golden,
// substitutes it into kernelTemplate, and asks the real GPU (via naga) to build a
// compute pipeline. Any WGSL the transpiler emits that naga rejects fails here.
// Guarded by requireGPU; on this machine it must PASS (not skip).
func TestTranspileCorpusCompiles(t *testing.T) {
	requireGPU(t)
	for _, name := range corpusNames {
		t.Run(name, func(t *testing.T) {
			glsl := readGLSLGolden(t, name)
			sdfWGSL, err := transpileGLSLToWGSL(glsl)
			if err != nil {
				t.Fatalf("transpile %s: %v", name, err)
			}
			wgsl := strings.Replace(kernelTemplate, "%SDF%", sdfWGSL, 1)
			if err := compileKernel(wgsl); err != nil {
				t.Fatalf("compile %s: %v\n--- WGSL ---\n%s", name, err, sdfWGSL)
			}
		})
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
