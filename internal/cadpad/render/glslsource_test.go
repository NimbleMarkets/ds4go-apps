package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// corpus is the exact set of shapes whose GLSL we transpile. Keep in sync with
// internal/cadpad/lua/bind.go primitives + ops.
var corpus = map[string]simplesdf.SDF3{
	"sphere":    simplesdf.Sphere(3),
	"box":       simplesdf.Box(4, 3, 2, 0),
	"box_round": simplesdf.Box(4, 3, 2, 0.5),
	"cylinder":  simplesdf.Cylinder(2, 5, 0),
	"torus":     simplesdf.Torus(3, 1),
	"hexprism":  simplesdf.HexPrism(2, 4),
	"triprism":  simplesdf.TriPrism(2, 4),
	"boxframe":  simplesdf.BoxFrame(4, 3, 2, 0.3),
	"union":     simplesdf.Sphere(3).Union(simplesdf.Box(2, 2, 2, 0)),
	"diff":      simplesdf.Sphere(3).Diff(simplesdf.Box(2, 2, 2, 0)),
	"translate": simplesdf.Sphere(3).Translate(1, 2, 3),
}

func TestCaptureGLSLGoldens(t *testing.T) {
	dir := filepath.Join("testdata", "glsl")
	for name, s := range corpus {
		glsl, _, err := generatedGLSL(s)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		golden := filepath.Join(dir, name+".glsl")
		if os.Getenv("UPDATE_GOLDEN") != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("%s: mkdir: %v", name, err)
			}
			if err := os.WriteFile(golden, []byte(glsl), 0o644); err != nil {
				t.Fatalf("%s: write golden: %v", name, err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%s: read golden (run with UPDATE_GOLDEN=1 first): %v", name, err)
		}
		if string(want) != glsl {
			t.Errorf("%s GLSL changed:\n--- got ---\n%s", name, glsl)
		}
	}
}
