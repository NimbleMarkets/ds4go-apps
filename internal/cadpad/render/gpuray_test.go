package render

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"
	"testing"
	"unsafe"
)

// spikeSphereSDF is a hardcoded WGSL sdf used only by the Phase 0 spike test.
const spikeSphereSDF = `fn sdf(p: vec3<f32>) -> f32 { return length(p) - 3.0; }`

// lum returns the perceptual-ish luminance of a pixel in [0,1].
func lum(c color.NRGBA) float64 {
	return (0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)) / 255.0
}

// writePNG dumps an image for eyeballing the spike under DUMP_PNG.
func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	t.Logf("wrote %s", path)
}

// spikeFrontCam builds a camera on +Z looking at the origin, with a fov wide
// enough that a radius-3 sphere at distance 10 fills a good fraction of frame.
func spikeFrontCam(w, h int) gpuCam {
	return gpuCam{
		Eye:        [3]float32{0, 0, 10},
		Fwd:        [3]float32{0, 0, -1},
		Right:      [3]float32{1, 0, 0},
		Up:         [3]float32{0, 1, 0},
		TanHalfFov: float32(math.Tan(20 * math.Pi / 180)),
		W:          uint32(w),
		H:          uint32(h),
		Samples:    1,
		MaxSteps:   80,
		Eps:        0.002,
		FarT:       100,
	}
}

// TestGPUCamLayout guards the std140-ish uniform layout. WGSL Cam is 96 bytes
// (6 x 16-byte blocks: 4 padded vec3 rows, the w/h/samples/maxSteps row, and the
// eps/farT/pad/pad quality row); a mismatch here is the first suspect for a
// missing or misplaced sphere.
func TestGPUCamLayout(t *testing.T) {
	if got := unsafe.Sizeof(gpuCam{}); got != 96 {
		t.Fatalf("unsafe.Sizeof(gpuCam{}) = %d, want 96 (must match WGSL Cam, multiple of 16)", got)
	}
}

func TestSpikeSphereRaymarch(t *testing.T) {
	requireGPU(t)
	wgsl := strings.Replace(kernelTemplate, "%SDF%", spikeSphereSDF, 1)
	cam := spikeFrontCam(128, 128)
	img, err := dispatchKernel(wgsl, cam, 128, 128)
	if err != nil {
		t.Fatalf("dispatchKernel: %v", err)
	}
	if os.Getenv("DUMP_PNG") != "" {
		writePNG(t, "testdata/spike_sphere.png", img)
	}
	// A centered sphere must light the center pixel and leave a corner dark.
	if c := img.NRGBAAt(64, 64); lum(c) < 0.3 {
		t.Errorf("center pixel not lit: %v lum=%.3f", c, lum(c))
	}
	if c := img.NRGBAAt(2, 2); lum(c) > 0.2 {
		t.Errorf("corner pixel should be background: %v lum=%.3f", c, lum(c))
	}
}
