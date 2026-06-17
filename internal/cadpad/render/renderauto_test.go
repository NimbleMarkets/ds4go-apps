package render

import (
	"testing"

	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// TestRenderModeString locks the status-line spellings.
func TestRenderModeString(t *testing.T) {
	if RenderModeGPU.String() != "GPU" {
		t.Errorf("RenderModeGPU.String() = %q, want GPU", RenderModeGPU.String())
	}
	if RenderModeCPU.String() != "CPU" {
		t.Errorf("RenderModeCPU.String() = %q, want CPU", RenderModeCPU.String())
	}
}

// TestRenderAngledAutoUsesGPU verifies that on a GPU machine, a normal sphere
// (which transpiles) is rendered on the GPU and returns a valid, correctly-sized,
// lit image.
func TestRenderAngledAutoUsesGPU(t *testing.T) {
	requireGPU(t)

	r, err := NewRenderer(PreviewConfig{})
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	cp := CameraParams{Azimuth: 0.6, Elevation: 0.4, Zoom: 1}
	const maxW, maxH, ds = 128, 128, 1

	img, rect, mode, err := r.RenderAngledAuto(simplesdf.Sphere(3), "auto_sphere", cp, maxW, maxH, ds)
	if err != nil {
		t.Fatalf("RenderAngledAuto: %v", err)
	}
	if mode != RenderModeGPU {
		t.Fatalf("mode = %v, want GPU (sphere transpiles and a GPU is present)", mode)
	}
	if rect.Dx() != 128 || rect.Dy() != 128 {
		t.Errorf("rect = %v, want 128x128", rect)
	}
	if img.Bounds() != rect {
		t.Errorf("img.Bounds() %v != rect %v", img.Bounds(), rect)
	}
	if !litCenter(img) {
		t.Errorf("GPU sphere center not lit (blank image?)")
	}
}

// TestRenderAngledAutoFallsBack forces the GPU path to fail deterministically and
// without depending on a GPU: a Twist shape's generated GLSL uses cos/sin, which
// the transpiler does not map, so RenderAngledGPU returns a transpile error.
// RenderAngledAuto must then fall back to the CPU mesh preview, returning
// RenderModeCPU with a valid image and a nil error (a fallback is never fatal).
//
// On a machine with NO GPU, gpuAvailable() is false and RenderAngledAuto skips
// the GPU path entirely — also yielding RenderModeCPU — so this assertion holds
// in both environments. Either way the real fallback branch is exercised: when a
// GPU IS present, the GPU path is entered and fails on the unsupported op,
// proving the error-to-CPU fall-through; the unsupported-op classification is
// pinned independently by TestUnsupportedOpsFallBack.
func TestRenderAngledAutoFallsBack(t *testing.T) {
	r, err := NewRenderer(PreviewConfig{})
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	cp := CameraParams{Azimuth: 0.6, Elevation: 0.4, Zoom: 1}
	const maxW, maxH, ds = 96, 96, 1

	// Twist: gsdf emits cos()/sin(), which the transpiler rejects -> GPU errors.
	twist := simplesdf.Box(4, 3, 2, 0).Twist(0.3)

	// Sanity: confirm this really is a GPU-failing op (independent of any GPU),
	// so the test stays meaningful even if the transpiler later gains cos/sin.
	if _, werr := transpileShape(twist); werr == nil {
		t.Skip("Twist now transpiles; pick another unsupported op for the fallback test")
	}

	img, rect, mode, err := r.RenderAngledAuto(twist, "auto_twist", cp, maxW, maxH, ds)
	if err != nil {
		t.Fatalf("RenderAngledAuto fallback returned fatal error: %v", err)
	}
	if mode != RenderModeCPU {
		t.Fatalf("mode = %v, want CPU (Twist must fall back)", mode)
	}
	if rect.Empty() || img.Bounds() != rect {
		t.Errorf("fallback image invalid: rect %v, bounds %v", rect, img.Bounds())
	}
}

// unsupportedOps are SDF3 operations whose generated GLSL the transpiler rejects
// (unsupported builtins), so RenderAngledAuto must route them to the CPU. None of
// these are exposed in the Lua surface (lua/bind.go) today; they exist on SDF3
// and are validated here as the documented fallback set. The full Lua op surface
// is GPU-clean (see TestGPUParityOps), so the only fallbacks are these.
var unsupportedOps = map[string]simplesdf.SDF3{
	"twist":     simplesdf.Box(4, 3, 2, 0).Twist(0.3),                         // cos/sin
	"array":     simplesdf.Sphere(1).Array(2, 2, 2, 3, 3, 3),                  // round
	"circarray": simplesdf.Box(1, 1, 1, 0).Translate(3, 0, 0).CircArray(6, 6), // atan
}

// TestUnsupportedOpsFallBack pins the fallback classification: for every op whose
// GLSL the transpiler rejects, RenderAngledAuto returns RenderModeCPU with no
// fatal error. This both documents the fallback set and is the real (GPU-
// independent) fallback case backing Part A. It also guards against a future
// transpiler change silently turning a fallback into a (possibly wrong) GPU path
// without a parity test: if one of these starts transpiling, the sub-test logs it
// so it can be moved into the parity corpus.
func TestUnsupportedOpsFallBack(t *testing.T) {
	r, err := NewRenderer(PreviewConfig{})
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	cp := CameraParams{Azimuth: 0.6, Elevation: 0.4, Zoom: 1}
	const maxW, maxH, ds = 96, 96, 1

	for name, s := range unsupportedOps {
		t.Run(name, func(t *testing.T) {
			if _, werr := transpileShape(s); werr == nil {
				t.Fatalf("%s now transpiles; move it into the GPU parity corpus "+
					"(TestGPUParityOps) instead of asserting fallback", name)
			}
			_, _, mode, err := r.RenderAngledAuto(s, "unsupported_"+name, cp, maxW, maxH, ds)
			if err != nil {
				t.Fatalf("%s: RenderAngledAuto returned fatal error: %v", name, err)
			}
			if mode != RenderModeCPU {
				t.Errorf("%s: mode = %v, want CPU (transpile error must fall back)", name, mode)
			}
		})
	}
}

// transpileShape is a test helper that runs the same generatedGLSL ->
// transpileGLSLToWGSL pipeline RenderAngledGPU uses, returning the WGSL or the
// first error. It lets tests classify an op as GPU-clean vs. fall-back without a
// GPU.
func transpileShape(s simplesdf.SDF3) (string, error) {
	glsl, _, err := generatedGLSL(s)
	if err != nil {
		return "", err
	}
	return transpileGLSLToWGSL(glsl)
}
