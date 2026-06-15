package render

import (
	"errors"
	"fmt"
	"image"
	"strings"

	"github.com/chewxy/math32"
	"github.com/soypat/gsdf/gleval"
	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// RenderAngledGPU renders the same orbit-camera perspective view as
// RenderAngledScale, but evaluates the SDF on the GPU via a transpiled WGSL
// raymarch kernel. It derives the bounding box, output dimensions, camera, and
// shading identically to RenderAngledScale so that — on the same inputs — the
// GPU image matches the CPU sphere-tracer's framing and color (see
// TestGPUParityCorpus).
//
// On a transpiler error it returns that error directly; CPU fallback is the
// caller's responsibility (Task 3.2), not this method's.
func (r *Renderer) RenderAngledGPU(s3 simplesdf.SDF3, name string, cp CameraParams, maxW, maxH, downscale int) (image.Image, image.Rectangle, error) {
	if s3.Shader() == nil {
		return nil, image.Rectangle{}, errors.New("nil SDF3 shader")
	}

	// Output dimensions: identical to RenderAngledScale (pane budget + MaxEdge
	// cap, then the same downscale division and floor).
	w, h := computeTargetSizeAngled(maxW, maxH, r.cfg.MaxEdge)
	if downscale > 1 {
		w = max(8, w/downscale)
		h = max(8, h/downscale)
	}
	rect := image.Rect(0, 0, w, h)

	// Bounding box: derived from the SDF exactly as RenderAngledScale does.
	bb3cpu, err := gleval.NewCPUSDF3(s3.Shader())
	if err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("NewCPUSDF3 bounds check: %w", err)
	}
	bb := bb3cpu.Bounds()

	// Transpile the SDF tree to WGSL. Any error is returned to the caller.
	glsl, _, err := generatedGLSL(s3)
	if err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("generatedGLSL: %w", err)
	}
	sdfWGSL, err := transpileGLSLToWGSL(glsl)
	if err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("transpileGLSLToWGSL: %w", err)
	}
	wgsl := strings.Replace(kernelTemplate, "%SDF%", sdfWGSL, 1)

	// Camera: same resolution path as the CPU renderer.
	cam := cameraFor(bb, cp)
	gcam := gpuCam{
		Eye:        [3]float32{cam.eye.X, cam.eye.Y, cam.eye.Z},
		Fwd:        [3]float32{cam.forward.X, cam.forward.Y, cam.forward.Z},
		Right:      [3]float32{cam.right.X, cam.right.Y, cam.right.Z},
		Up:         [3]float32{cam.up.X, cam.up.Y, cam.up.Z},
		TanHalfFov: math32.Tan(cam.fov / 2),
		W:          uint32(w),
		H:          uint32(h),
	}

	img, err := dispatchKernel(wgsl, gcam, w, h)
	if err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("dispatchKernel: %w", err)
	}
	return img, rect, nil
}
