package render

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"testing"
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
