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

	// Get-or-build the cached pipeline AND dispatch it inside ONE executor
	// closure (gpuDo). Bundling them on the single GPU thread is what closes
	// the use-after-Release window: a concurrent Invalidate/ClearCache enqueues
	// its Release on the same executor, so it can only run BEFORE or AFTER this
	// whole build+dispatch, never mid-dispatch.
	//
	// The transpile above is pure Go and runs off the executor. The cache map
	// bookkeeping (gpuPipelineLookup / gpuPipelineStore) takes Renderer.mu but
	// makes no GPU calls, and crucially we never hold mu across gpuDo — so
	// there is no lock-order inversion with the executor.
	var img image.Image
	err = gpuDo(func() error {
		// Inside the executor: never call gpuDo again (re-entrant deadlock).
		// Call the raw build/dispatch helpers directly.
		pipe, hit := r.gpuPipelineLookup(name, wgsl)
		if !hit {
			var berr error
			pipe, berr = buildGPUPipeline(wgsl)
			if berr != nil {
				return fmt.Errorf("build gpu pipeline: %w", berr)
			}
			// Publish the freshly built pipeline; gpuPipelineStore returns the
			// stale entry it displaced (if any), which we Release here on the
			// executor thread, after it is out of the map.
			if old := r.gpuPipelineStore(name, wgsl, pipe); old != nil && old != pipe {
				old.Release()
			}
		}
		di, derr := dispatchPipeline(pipe, gcam, w, h)
		if derr != nil {
			return fmt.Errorf("dispatchPipeline: %w", derr)
		}
		img = di
		return nil
	})
	if err != nil {
		return nil, image.Rectangle{}, err
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

// wgslHash returns the cache key hash for a WGSL kernel string.
func wgslHash(wgsl string) string {
	sum := sha256.Sum256([]byte(wgsl))
	return hex.EncodeToString(sum[:])
}

// gpuPipelineLookup returns the cached pipeline for name when its WGSL hash
// matches (hit=true). On a miss it bumps compileCount and returns (nil, false),
// signaling the caller to build. It only touches the cache map under mu and
// makes no GPU calls, so it is safe to call from inside a gpuDo closure (the
// caller does) without risking a lock-order inversion — mu is released before
// any GPU work and never held across gpuDo.
//
// compileCount is bumped here (on the miss that schedules a build) rather than
// after the build so TestPipelineCache's assertions hold; a failed build is not
// stored, but the count reflects the build attempt as before.
func (r *Renderer) gpuPipelineLookup(name, wgsl string) (*gpuPipeline, bool) {
	hash := wgslHash(wgsl)
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.gpuPipelines[name]; ok && c.wgslHash == hash {
		return c.pipe, true
	}
	r.compileCount++
	return nil, false
}

// gpuPipelineStore publishes a freshly built pipeline for name and returns the
// stale *gpuPipeline it displaced (or nil). The caller Releases the displaced
// pipeline AFTER it is out of the map (on the executor thread), so a concurrent
// render can never reuse a freed pipeline. Map-only; no GPU calls.
func (r *Renderer) gpuPipelineStore(name, wgsl string, pipe *gpuPipeline) *gpuPipeline {
	hash := wgslHash(wgsl)
	r.mu.Lock()
	defer r.mu.Unlock()
	var old *gpuPipeline
	if c := r.gpuPipelines[name]; c != nil {
		old = c.pipe
	}
	r.gpuPipelines[name] = &cachedGPUPipeline{wgslHash: hash, pipe: pipe}
	return old
}
