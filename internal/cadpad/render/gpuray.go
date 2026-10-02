package render

import (
	"fmt"
	"image"

	"github.com/NimbleMarkets/ds4go-apps/ntgpu"
	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
)

// gpuCam mirrors the WGSL Cam struct layout (std140-ish; each vec3<f32> is
// 16-byte aligned, so every vec3 is padded to a full vec4 with an explicit
// trailing float). The (w,h) pair shares its 16-byte block with the quality
// knobs Samples/MaxSteps, and a final 16-byte block carries Eps/FarT plus two
// pad floats. unsafe.Sizeof(gpuCam{}) must be 96 (6 x 16-byte blocks) and match
// the WGSL struct byte-for-byte, or the uniform upload will be misinterpreted.
//
// Quality fields: Samples is the ordered-grid SSAA side count (1 → 1 ray at the
// pixel center, 2 → 2x2=4 rays, 3 → 3x3=9 rays). MaxSteps/Eps/FarT are the
// raymarch step budget, hit epsilon, and far cutoff respectively. The defaults
// (Samples=1, MaxSteps=80, Eps=0.002, FarT=100) reproduce the original kernel.
type gpuCam struct {
	Eye        [3]float32
	_p0        float32
	Fwd        [3]float32
	_p1        float32
	Right      [3]float32
	_p2        float32
	Up         [3]float32
	TanHalfFov float32
	W, H       uint32
	Samples    uint32
	MaxSteps   uint32
	Eps, FarT  float32
	_q0, _q1   float32
}

// gpuPipeline holds the geometry-dependent GPU objects produced by compiling a
// raymarch WGSL string: the shader module, the bind group layout, the pipeline
// layout, and the compute pipeline. These are expensive to build (naga compiles
// WGSL→MSL on CreateShaderModule, and the driver builds the pipeline) but only
// change when the SDF/geometry changes — not per camera frame. The Renderer
// caches one of these per object so interactive camera moves reuse it. All four
// objects are live GPU resources; call Release() exactly once when done.
type gpuPipeline struct {
	shader *wgpu.ShaderModule
	bgl    *wgpu.BindGroupLayout
	pl     *wgpu.PipelineLayout
	pipe   *wgpu.ComputePipeline
}

// Release frees all GPU objects held by the pipeline. It is safe to call on a
// partially-built pipeline (nil fields are skipped) and must not be called more
// than once on the same instance.
func (p *gpuPipeline) Release() {
	if p == nil {
		return
	}
	if p.pipe != nil {
		p.pipe.Release()
		p.pipe = nil
	}
	if p.pl != nil {
		p.pl.Release()
		p.pl = nil
	}
	if p.bgl != nil {
		p.bgl.Release()
		p.bgl = nil
	}
	if p.shader != nil {
		p.shader.Release()
		p.shader = nil
	}
}

// buildGPUPipeline compiles the given raymarch WGSL into a reusable gpuPipeline.
// This is the naga-compile + pipeline-build cost; the result is cacheable and
// reused across dispatches (different cameras/sizes) of the same geometry.
// On any error the partially-built objects are released before returning.
//
// It calls gogpu directly and MUST run on the executor goroutine (inside a
// gpuDo closure) — never call it via gpuDo, which would deadlock re-entrantly.
func buildGPUPipeline(wgsl string) (*gpuPipeline, error) {
	dev, err := device()
	if err != nil {
		return nil, err
	}
	p := &gpuPipeline{}

	p.shader, err = dev.CreateShaderModule(&wgpu.ShaderModuleDescriptor{Label: "ray", WGSL: wgsl})
	if err != nil {
		p.Release()
		return nil, fmt.Errorf("create shader: %w", err)
	}

	p.bgl, err = dev.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Entries: []gputypes.BindGroupLayoutEntry{
			{
				Binding:    0,
				Visibility: wgpu.ShaderStageCompute,
				Buffer:     &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeUniform},
			},
			{
				Binding:    1,
				Visibility: wgpu.ShaderStageCompute,
				Buffer:     &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeStorage},
			},
		},
	})
	if err != nil {
		p.Release()
		return nil, fmt.Errorf("create bind group layout: %w", err)
	}

	p.pl, err = dev.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		BindGroupLayouts: []*wgpu.BindGroupLayout{p.bgl},
	})
	if err != nil {
		p.Release()
		return nil, fmt.Errorf("create pipeline layout: %w", err)
	}

	p.pipe, err = dev.CreateComputePipeline(&wgpu.ComputePipelineDescriptor{
		Layout: p.pl, Module: p.shader, EntryPoint: "main",
	})
	if err != nil {
		p.Release()
		return nil, fmt.Errorf("create compute pipeline: %w", err)
	}
	return p, nil
}

// dispatchPipeline runs a previously-built (and possibly cached) gpuPipeline
// over a w*h grid with the supplied camera uniform and returns the rgba8 result
// as an image.NRGBA. It creates only the per-dispatch resources (uniform buffer,
// output + staging buffers, bind group, command encoder) and releases them
// before returning; the pipeline's cached objects (shader/BGL/pipeline-layout/
// pipeline) are left intact for reuse.
//
// The output is an array<u32> storage buffer (one packed rgba8 per pixel).
// Storage buffers are not directly mappable, so the result is copied into a
// MapRead staging buffer (CopyBufferToBuffer before Finish) and that is mapped
// for readback by ntgpu.DispatchRGBA8.
//
// It calls gogpu directly and MUST run on the executor goroutine (inside a
// gpuDo closure) — never call it via gpuDo, which would deadlock re-entrantly.
func dispatchPipeline(p *gpuPipeline, cam gpuCam, w, h int) (*image.NRGBA, error) {
	if w <= 0 || h <= 0 {
		// A collapsed viewport (e.g. a bubbletea pane at zero size during
		// resize) is a legitimate per-frame input; avoid zero-sized buffers.
		return image.NewNRGBA(image.Rect(0, 0, max(w, 0), max(h, 0))), nil
	}
	dev, err := device()
	if err != nil {
		return nil, err
	}
	img, err := ntgpu.DispatchRGBA8(dev, ntgpu.ImageDispatch{
		Label:           "ray",
		Width:           w,
		Height:          h,
		WorkgroupX:      8,
		WorkgroupY:      8,
		Uniform:         ntgpu.BytesOf(&cam),
		BindGroupLayout: p.bgl,
		Pipeline:        p.pipe,
		UniformBinding:  0,
		OutputBinding:   1,
	})
	if err != nil {
		return nil, err
	}
	// pack() writes u32 = R | G<<8 | B<<16 | 255<<24; on little-endian targets
	// (arm64/amd64) that's bytes [R,G,B,A], exactly NRGBA.Pix order. Alpha is a
	// constant 255, so NRGBA's non-premultiplied contract is satisfied.
	return img, nil
}
