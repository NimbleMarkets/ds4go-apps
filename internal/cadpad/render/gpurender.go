package render

import (
	"crypto/sha256"
	"encoding/hex"
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

	// Get (or build) the cached compute pipeline for this object. The pipeline
	// only depends on the WGSL (geometry); the camera lives entirely in the
	// per-dispatch uniform buffer. Re-renders of unchanged geometry skip the
	// naga WGSL→MSL compile.
	pipe, err := r.gpuPipelineFor(name, wgsl)
	if err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("build gpu pipeline: %w", err)
	}

	img, err := dispatchPipeline(pipe, gcam, w, h)
	if err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("dispatchPipeline: %w", err)
	}
	return img, rect, nil
}

// gpuCompileCount returns how many times a GPU pipeline has actually been built
// (cache misses). Read under the cache mutex so tests don't race the render.
func (r *Renderer) gpuCompileCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.compileCount
}

// gpuPipelineFor returns the compiled compute pipeline for the named object,
// building (and caching) one if absent or if the WGSL hash differs from the
// cached entry. When an entry is replaced because the geometry changed, the old
// pipeline is Released only after it has been removed from the map, so a
// subsequent render can never reuse a freed pipeline. compileCount is bumped
// only on an actual build, which TestPipelineCache asserts on.
//
// Renders are sequential today, but the cache is mutex-guarded (matching the
// mesh cache) so the bookkeeping stays correct if a future task adds a GPU
// dispatch goroutine.
func (r *Renderer) gpuPipelineFor(name, wgsl string) (*gpuPipeline, error) {
	sum := sha256.Sum256([]byte(wgsl))
	hash := hex.EncodeToString(sum[:])

	r.mu.Lock()
	if c, ok := r.gpuPipelines[name]; ok && c.wgslHash == hash {
		pipe := c.pipe
		r.mu.Unlock()
		return pipe, nil
	}
	r.compileCount++
	r.mu.Unlock()

	// Build outside the lock (CreateShaderModule/CreateComputePipeline are slow
	// and we don't want to serialize unrelated cache reads behind a compile).
	pipe, err := buildGPUPipeline(wgsl)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	old := r.gpuPipelines[name]
	r.gpuPipelines[name] = &cachedGPUPipeline{wgslHash: hash, pipe: pipe}
	r.mu.Unlock()

	// Release the displaced entry (stale geometry) after it is out of the map.
	if old != nil && old.pipe != pipe {
		old.pipe.Release()
	}
	return pipe, nil
}
