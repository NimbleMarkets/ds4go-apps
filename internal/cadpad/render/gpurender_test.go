package render

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// meanAbsDiff returns the mean absolute per-channel difference between two
// images over the RGB channels, normalized to [0,1]. Images must share bounds.
func meanAbsDiff(a, b image.Image) float64 {
	ba, bb := a.Bounds(), b.Bounds()
	if ba != bb {
		panic(fmt.Sprintf("meanAbsDiff: bounds differ %v vs %v", ba, bb))
	}
	var sum float64
	var n int
	for y := ba.Min.Y; y < ba.Max.Y; y++ {
		for x := ba.Min.X; x < ba.Max.X; x++ {
			ca := color.NRGBAModel.Convert(a.At(x, y)).(color.NRGBA)
			cb := color.NRGBAModel.Convert(b.At(x, y)).(color.NRGBA)
			sum += absDiff(ca.R, cb.R)
			sum += absDiff(ca.G, cb.G)
			sum += absDiff(ca.B, cb.B)
			n += 3
		}
	}
	if n == 0 {
		return 0
	}
	return (sum / float64(n)) / 255.0
}

func absDiff(a, b uint8) float64 {
	if a > b {
		return float64(a - b)
	}
	return float64(b - a)
}

// TestGPUParityCorpus renders every corpus shape via both the GPU raymarcher
// (RenderAngledGPU) and the CPU sphere-tracer (RenderAngledScale) at the same
// size and camera, and asserts the mean-absolute per-pixel difference stays
// under tolerance. A rotated/mirrored/offset shape would blow this up — that is
// a camera bug, not a tolerance problem. The tolerance only absorbs f32-vs-f64
// drift and silhouette aliasing along the edge.
func TestGPUParityCorpus(t *testing.T) {
	requireGPU(t)

	cp := CameraParams{Azimuth: 0.6, Elevation: 0.4, Zoom: 1}
	const maxW, maxH, downscale = 128, 128, 1
	const tol = 0.06

	r, err := NewRenderer(PreviewConfig{})
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	dump := os.Getenv("DUMP_PNG") != ""

	// Iterate deterministically; corpus is the shared map from glslsource_test.go.
	names := []string{
		"sphere", "box", "box_round", "cylinder", "torus", "hexprism",
		"triprism", "boxframe", "union", "diff", "translate",
	}
	for _, name := range names {
		s3 := corpus[name]
		t.Run(name, func(t *testing.T) {
			gpuImg, gpuRect, err := r.RenderAngledGPU(s3, name, cp, maxW, maxH, downscale)
			if err != nil {
				t.Fatalf("RenderAngledGPU: %v", err)
			}
			cpuImg, cpuRect, err := r.RenderAngledScale(s3, name, cp, maxW, maxH, downscale)
			if err != nil {
				t.Fatalf("RenderAngledScale: %v", err)
			}
			if gpuRect != cpuRect {
				t.Fatalf("rect mismatch: gpu %v cpu %v", gpuRect, cpuRect)
			}
			d := meanAbsDiff(gpuImg, cpuImg)
			t.Logf("%s: meanAbsDiff=%.4f (tol %.4f)", name, d, tol)
			if d > tol {
				if dump {
					writePNG(t, "testdata/parity_"+name+"_gpu.png", gpuImg)
					writePNG(t, "testdata/parity_"+name+"_cpu.png", cpuImg)
				}
				t.Errorf("%s: meanAbsDiff %.4f exceeds tol %.4f", name, d, tol)
			} else if dump {
				writePNG(t, "testdata/parity_"+name+"_gpu.png", gpuImg)
				writePNG(t, "testdata/parity_"+name+"_cpu.png", cpuImg)
			}
		})
	}
}

// gpuParityOps is the full set of Lua SDF operations (lua/bind.go) that were not
// in the original glsl-golden corpus, each as a representative shape with a
// non-axis-aligned camera so a transposed/mirrored/mis-scaled result would blow
// up the per-pixel diff. Every entry is expected to render on the GPU at parity
// with the CPU sphere-tracer:
//
//   - intersect, xor, smooth-min (K): boolean ops.
//   - scale, offset, shell, elongate: modifiers.
//   - rotate / rotate_x/y/z: mat4 transforms — these emit `mat4 invT=mat4(...)`
//     and `((invT)*vec4(p,0)).xyz`, which the transpiler now supports (mat4 type
//     + a hoist of the swizzle-of-parenthesized-expr that naga's MSL backend
//     miscompiles). A transposed rotation matrix would show here as a wrongly
//     oriented box and fail parity; ~0.002 diffs confirm correct orientation.
//
// This is the "nothing renders silently wrong" gate for the Lua op surface.
var gpuParityOps = map[string]simplesdf.SDF3{
	"intersect":    simplesdf.Sphere(3).Intersect(simplesdf.Box(3, 3, 3, 0)),
	"xor":          simplesdf.Sphere(3).Xor(simplesdf.Box(3, 3, 3, 0)),
	"smooth_union": simplesdf.Sphere(3).K(0.5).Union(simplesdf.Box(3, 3, 3, 0).Translate(2, 0, 0)),
	// smooth_diff / smooth_intersect exercise the smooth-min blend on the other
	// two booleans; smooth_union_chain nests the smooth-min across two unions.
	"smooth_diff":      simplesdf.Sphere(3).K(0.5).Diff(simplesdf.Box(2, 2, 2, 0)),
	"smooth_intersect": simplesdf.Sphere(3).K(0.5).Intersect(simplesdf.Box(2.5, 2.5, 2.5, 0)),
	"smooth_union_chain": simplesdf.Sphere(2).K(0.4).Union(simplesdf.Box(2, 2, 2, 0)).
		K(0.4).Union(simplesdf.Sphere(2).Translate(2, 0, 0)),
	"scale": simplesdf.Box(4, 3, 2, 0).Scale(1.5),
	"offset":       simplesdf.Box(4, 3, 2, 0).Offset(0.3),
	"shell":        simplesdf.Sphere(3).Shell(0.2),
	"elongate":     simplesdf.Box(2, 2, 2, 0).Elongate(2, 0, 0),
	"rotate":       simplesdf.Box(4, 3, 2, 0).Rotate(0.6, 0, 0, 1),
	"rotate_x":     simplesdf.Box(4, 3, 2, 0).RotateX(0.6),
	"rotate_y":     simplesdf.Box(4, 3, 2, 0).RotateY(0.6),
	"rotate_z":     simplesdf.Box(4, 3, 2, 0).RotateZ(0.6),
}

// TestGPUParityOps validates the GPU path for the FULL Lua op surface beyond the
// glsl-golden corpus. Each op is rendered via the GPU raymarcher and the CPU
// sphere-tracer at the same size/camera; the mean-abs per-pixel diff must stay
// under tolerance. A transpile-but-wrong op (the dangerous case) would fail here.
// Ops that DON'T transpile are not in this map — they are covered by
// TestUnsupportedOpsFallBack instead.
func TestGPUParityOps(t *testing.T) {
	requireGPU(t)

	cp := CameraParams{Azimuth: 0.6, Elevation: 0.4, Zoom: 1}
	const maxW, maxH, downscale = 128, 128, 1
	const tol = 0.06

	r, err := NewRenderer(PreviewConfig{})
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	// Deterministic order.
	names := []string{
		"intersect", "xor", "smooth_union", "smooth_diff", "smooth_intersect",
		"smooth_union_chain", "scale", "offset", "shell",
		"elongate", "rotate", "rotate_x", "rotate_y", "rotate_z",
	}
	for _, name := range names {
		s3 := gpuParityOps[name]
		t.Run(name, func(t *testing.T) {
			gpuImg, gpuRect, err := r.RenderAngledGPU(s3, "ops_"+name, cp, maxW, maxH, downscale)
			if err != nil {
				t.Fatalf("RenderAngledGPU: %v", err)
			}
			cpuImg, cpuRect, err := r.RenderAngledScale(s3, "ops_"+name, cp, maxW, maxH, downscale)
			if err != nil {
				t.Fatalf("RenderAngledScale: %v", err)
			}
			if gpuRect != cpuRect {
				t.Fatalf("rect mismatch: gpu %v cpu %v", gpuRect, cpuRect)
			}
			d := meanAbsDiff(gpuImg, cpuImg)
			t.Logf("%s: meanAbsDiff=%.4f (tol %.4f)", name, d, tol)
			if d > tol {
				if os.Getenv("DUMP_PNG") != "" {
					writePNG(t, "testdata/parity_op_"+name+"_gpu.png", gpuImg)
					writePNG(t, "testdata/parity_op_"+name+"_cpu.png", cpuImg)
				}
				t.Errorf("%s: meanAbsDiff %.4f exceeds tol %.4f (silent-wrong render?)", name, d, tol)
			}
		})
	}
}

// litCenter reports whether the center of img is brighter than its corner,
// i.e. a shape silhouette is present (sanity check that the render produced a
// real image, not a blank/background frame).
func litCenter(img image.Image) bool {
	b := img.Bounds()
	cx, cy := (b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2
	center := color.NRGBAModel.Convert(img.At(cx, cy)).(color.NRGBA)
	corner := color.NRGBAModel.Convert(img.At(b.Min.X+1, b.Min.Y+1)).(color.NRGBA)
	return lum(center) > lum(corner)+0.1
}

// TestPipelineCache proves the GPU compute-pipeline cache: rendering the same
// geometry twice compiles the WGSL→MSL pipeline exactly once, while a geometry
// edit (via Invalidate, or a changed SDF under the same name) forces a
// recompile. It also checks that every render — cached or rebuilt — still
// produces a correct, lit image at the requested dimensions.
func TestPipelineCache(t *testing.T) {
	requireGPU(t)

	cp := CameraParams{Azimuth: 0.6, Elevation: 0.4, Zoom: 1}
	const maxW, maxH, downscale = 128, 128, 1
	const name = "cache_sphere"

	r, err := NewRenderer(PreviewConfig{})
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	sphere := simplesdf.Sphere(3)

	// First render: cold cache → exactly one compile.
	img1, rect1, err := r.RenderAngledGPU(sphere, name, cp, maxW, maxH, downscale)
	if err != nil {
		t.Fatalf("render 1: %v", err)
	}
	if got := r.gpuCompileCount(); got != 1 {
		t.Fatalf("after render 1: compileCount = %d, want 1", got)
	}
	if rect1.Dx() != 128 || rect1.Dy() != 128 {
		t.Fatalf("render 1 rect %v, want 128x128", rect1)
	}
	if !litCenter(img1) {
		t.Errorf("render 1: sphere center not lit (blank image?)")
	}

	// Second render: same geometry/name, different camera → cache HIT, no
	// recompile. compileCount must stay at 1.
	cp2 := CameraParams{Azimuth: 1.2, Elevation: 0.2, Zoom: 1}
	img2, _, err := r.RenderAngledGPU(sphere, name, cp2, maxW, maxH, downscale)
	if err != nil {
		t.Fatalf("render 2: %v", err)
	}
	if got := r.gpuCompileCount(); got != 1 {
		t.Errorf("after render 2 (same geometry): compileCount = %d, want 1 (cache should have hit)", got)
	}
	if !litCenter(img2) {
		t.Errorf("render 2: sphere center not lit")
	}

	// Invalidate → next render must rebuild the pipeline (compileCount 1→2).
	r.Invalidate(name)
	img3, _, err := r.RenderAngledGPU(sphere, name, cp, maxW, maxH, downscale)
	if err != nil {
		t.Fatalf("render 3 (post-invalidate): %v", err)
	}
	if got := r.gpuCompileCount(); got != 2 {
		t.Errorf("after Invalidate + render: compileCount = %d, want 2 (should recompile)", got)
	}
	if !litCenter(img3) {
		t.Errorf("render 3: sphere center not lit")
	}

	// Changed SDF under the SAME name → WGSL hash differs → recompile (2→3),
	// even without an explicit Invalidate. The box render also confirms the
	// rebuilt-pipeline path still produces a correct, lit image (full per-pixel
	// CPU parity is covered by TestGPUParityCorpus; here we keep the GPU dispatch
	// count low to avoid aggravating the known Metal autorelease-pool teardown
	// flake).
	box := simplesdf.Box(4, 3, 2, 0)
	img4, rect4, err := r.RenderAngledGPU(box, name, cp, maxW, maxH, downscale)
	if err != nil {
		t.Fatalf("render 4 (changed geometry): %v", err)
	}
	if got := r.gpuCompileCount(); got != 3 {
		t.Errorf("after changed SDF same name: compileCount = %d, want 3 (hash change should recompile)", got)
	}
	if rect4.Dx() != 128 || rect4.Dy() != 128 {
		t.Errorf("render 4 rect %v, want 128x128", rect4)
	}
	if !litCenter(img4) {
		t.Errorf("render 4: box center not lit")
	}

	// ClearCache must Release every remaining cached pipeline without panicking
	// or double-freeing. (No extra GPU dispatch here, to keep teardown pressure
	// low.) The earlier Invalidate already exercised single-entry Release.
	r.ClearCache()
	r.mu.Lock()
	n := len(r.gpuPipelines)
	r.mu.Unlock()
	if n != 0 {
		t.Errorf("after ClearCache: %d cached pipelines remain, want 0", n)
	}
}
