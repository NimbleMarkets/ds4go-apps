package render

import (
	"image"

	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// RenderMode reports which renderer produced a frame, for the status line.
type RenderMode int

const (
	// RenderModeGPU means the frame came from the GPU raymarcher.
	RenderModeGPU RenderMode = iota
	// RenderModeCPU means the frame came from the CPU mesh preview (fallback,
	// or because no GPU is present).
	RenderModeCPU
)

// String renders the mode as "GPU"/"CPU" for display.
func (m RenderMode) String() string {
	switch m {
	case RenderModeGPU:
		return "GPU"
	case RenderModeCPU:
		return "CPU"
	default:
		return "?"
	}
}

// RenderAngledAuto renders via the GPU raymarcher when a GPU is available and
// the SDF transpiles+dispatches; otherwise (no GPU, or any transpile/build/
// dispatch failure) it falls back to the CPU mesh preview. A fallback is never a
// fatal error: an SDF whose generated GLSL the transpiler rejects (e.g. an op
// using a builtin we don't yet map, like Twist's cos/sin) renders on the CPU
// rather than failing. The returned RenderMode tells the caller which path ran.
//
// The CPU fallback uses RenderAngledMesh (the fast mesh preview), matching the
// live viewport's existing CPU path; its error (a genuine mesh/raster failure)
// is surfaced, since at that point there is no further fallback.
func (r *Renderer) RenderAngledAuto(s3 simplesdf.SDF3, name string, cp CameraParams, maxW, maxH, downscale int) (image.Image, image.Rectangle, RenderMode, error) {
	if gpuAvailable() {
		img, rect, err := r.RenderAngledGPU(s3, name, cp, maxW, maxH, downscale)
		if err == nil {
			return img, rect, RenderModeGPU, nil
		}
		// transpile/build/dispatch failure: fall through to CPU, do not surface
		// as fatal. (The named pipeline cache is unaffected: a failed build is
		// never stored, so a later geometry that does transpile still caches.)
	}
	img, rect, err := r.RenderAngledMesh(s3, name, cp, maxW, maxH, downscale)
	return img, rect, RenderModeCPU, err
}
