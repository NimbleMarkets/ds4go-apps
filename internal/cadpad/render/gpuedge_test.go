package render

import (
	"testing"

	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// The GPU raymarch is cheap enough to render at display resolution, while the
// CPU fallbacks are not. GPUMaxEdge caps only the GPU path and defaults to
// twice MaxEdge so existing callers gain resolution without a config change.
func TestGPUMaxEdgeDefaultsToTwiceMaxEdge(t *testing.T) {
	if DefaultPreviewConfig.GPUMaxEdge != 2*DefaultPreviewConfig.MaxEdge {
		t.Fatalf("default GPUMaxEdge=%d MaxEdge=%d", DefaultPreviewConfig.GPUMaxEdge, DefaultPreviewConfig.MaxEdge)
	}
	r, err := NewRenderer(PreviewConfig{MaxEdge: 32, EvalBuffer: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if r.cfg.GPUMaxEdge != 64 {
		t.Fatalf("unset GPUMaxEdge resolved to %d, want 64", r.cfg.GPUMaxEdge)
	}
	r.SetMaxEdge(100)
	if r.cfg.GPUMaxEdge != 200 {
		t.Fatalf("SetMaxEdge left GPUMaxEdge at %d, want 200", r.cfg.GPUMaxEdge)
	}
}

func TestGPUMaxEdgeCapsOnlyGPUPath(t *testing.T) {
	requireGPU(t)
	r, err := NewRenderer(PreviewConfig{MaxEdge: 32, GPUMaxEdge: 64, EvalBuffer: 4096})
	if err != nil {
		t.Fatal(err)
	}
	sphere := simplesdf.Sphere(5)
	cp := CameraParams{Zoom: 1}
	_, gpuRect, err := r.RenderAngledGPU(sphere, "ball", cp, 200, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if gpuRect.Dx() != 64 || gpuRect.Dy() != 32 {
		t.Fatalf("GPU rect %+v, want 64x32", gpuRect)
	}
	_, cpuRect, err := r.RenderAngledMesh(sphere, "ball", cp, 200, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cpuRect.Dx() != 32 || cpuRect.Dy() != 16 {
		t.Fatalf("CPU rect %+v, want 32x16", cpuRect)
	}
}
