package render

import (
	"context"
	"fmt"
	"image"
	"unsafe"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
)

// gpuCam mirrors the WGSL Cam struct layout (std140-ish; each vec3<f32> is
// 16-byte aligned, so every vec3 is padded to a full vec4 with an explicit
// trailing float, and the (w,h) pair plus two u32 pads form a final 16-byte
// block). unsafe.Sizeof(gpuCam{}) must be 80 (a multiple of 16) and match the
// WGSL struct byte-for-byte, or the uniform upload will be misinterpreted.
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
	_p3, _p4   uint32
}

// compileKernel builds a shader module and compute pipeline from the given WGSL
// on the real device and returns any error, without dispatching. It is the
// correctness gate for the transpiler: a WGSL string that naga rejects fails at
// CreateShaderModule or CreateComputePipeline. The created objects are released
// before returning; only the error matters.
//
// The GPU work runs on the executor goroutine via gpuDo. The inner body calls
// gogpu directly (never gpuDo, which would deadlock re-entrantly).
func compileKernel(wgsl string) error {
	return gpuDo(func() error { return compileKernelOnExec(wgsl) })
}

// compileKernelOnExec is the raw body of compileKernel; it uses gogpu directly
// and MUST run on the executor goroutine (inside a gpuDo closure).
func compileKernelOnExec(wgsl string) error {
	dev, err := device()
	if err != nil {
		return err
	}
	shader, err := dev.CreateShaderModule(&wgpu.ShaderModuleDescriptor{Label: "compilecheck", WGSL: wgsl})
	if err != nil {
		return fmt.Errorf("create shader: %w", err)
	}
	defer shader.Release()
	pipe, err := dev.CreateComputePipeline(&wgpu.ComputePipelineDescriptor{
		Module: shader, EntryPoint: "main",
	})
	if err != nil {
		return fmt.Errorf("create compute pipeline: %w", err)
	}
	pipe.Release()
	return nil
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
		Entries: []wgpu.BindGroupLayoutEntry{
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

// dispatchKernel runs the given raymarch WGSL over a w*h grid with the supplied
// camera uniform and returns the rgba8 result as an image.NRGBA. It builds a
// throwaway pipeline (compiling the WGSL) and releases it after the dispatch;
// callers that re-render the same geometry should cache a gpuPipeline and call
// dispatchPipeline directly to skip the recompile (see RenderAngledGPU).
func dispatchKernel(wgsl string, cam gpuCam, w, h int) (*image.NRGBA, error) {
	if w <= 0 || h <= 0 {
		// A collapsed viewport (e.g. a bubbletea pane at zero size during
		// resize) is a legitimate per-frame input; avoid zero-sized buffers.
		return image.NewNRGBA(image.Rect(0, 0, max(w, 0), max(h, 0))), nil
	}
	// Build + dispatch + Release run as ONE executor closure so they share the
	// GPU thread and can't interleave with a concurrent Release. The inner
	// helpers call gogpu directly; they must not call gpuDo themselves.
	var img *image.NRGBA
	err := gpuDo(func() error {
		p, err := buildGPUPipeline(wgsl)
		if err != nil {
			return err
		}
		defer p.Release()
		img, err = dispatchPipeline(p, cam, w, h)
		return err
	})
	return img, err
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
// for readback — exactly as runComputeDouble does in gpudevice.go.
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
	q := dev.Queue()
	npix := w * h
	outBytes := uint64(npix * 4)

	camBytes := unsafe.Slice((*byte)(unsafe.Pointer(&cam)), int(unsafe.Sizeof(cam)))
	uni, err := dev.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "cam", Size: uint64(len(camBytes)),
		Usage: wgpu.BufferUsageUniform | wgpu.BufferUsageCopyDst,
	})
	if err != nil {
		return nil, fmt.Errorf("create uniform buffer: %w", err)
	}
	defer uni.Release()

	out, err := dev.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "out", Size: outBytes,
		Usage: wgpu.BufferUsageStorage | wgpu.BufferUsageCopySrc | wgpu.BufferUsageCopyDst,
	})
	if err != nil {
		return nil, fmt.Errorf("create output buffer: %w", err)
	}
	defer out.Release()

	staging, err := dev.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "staging", Size: outBytes,
		Usage: wgpu.BufferUsageMapRead | wgpu.BufferUsageCopyDst,
	})
	if err != nil {
		return nil, fmt.Errorf("create staging buffer: %w", err)
	}
	defer staging.Release()

	if err := q.WriteBuffer(uni, 0, camBytes); err != nil {
		return nil, fmt.Errorf("write uniform: %w", err)
	}

	bg, err := dev.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Layout: p.bgl,
		Entries: []wgpu.BindGroupEntry{
			{Binding: 0, Buffer: uni, Size: uint64(len(camBytes))},
			{Binding: 1, Buffer: out, Size: outBytes},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create bind group: %w", err)
	}
	defer bg.Release()

	enc, err := dev.CreateCommandEncoder(nil)
	if err != nil {
		return nil, fmt.Errorf("create command encoder: %w", err)
	}
	pass, err := enc.BeginComputePass(nil)
	if err != nil {
		return nil, fmt.Errorf("begin compute pass: %w", err)
	}
	pass.SetPipeline(p.pipe)
	pass.SetBindGroup(0, bg, nil)
	pass.Dispatch((uint32(w)+7)/8, (uint32(h)+7)/8, 1)
	if err := pass.End(); err != nil {
		return nil, fmt.Errorf("end compute pass: %w", err)
	}
	enc.CopyBufferToBuffer(out, 0, staging, 0, outBytes)
	cmd, err := enc.Finish()
	if err != nil {
		return nil, fmt.Errorf("finish: %w", err)
	}
	if _, err := q.Submit(cmd); err != nil {
		return nil, fmt.Errorf("submit: %w", err)
	}

	if err := staging.Map(context.Background(), wgpu.MapModeRead, 0, outBytes); err != nil {
		return nil, fmt.Errorf("map staging: %w", err)
	}
	defer staging.Unmap()
	rng, err := staging.MappedRange(0, outBytes)
	if err != nil {
		return nil, fmt.Errorf("mapped range: %w", err)
	}
	defer rng.Release()

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	// pack() writes u32 = R | G<<8 | B<<16 | 255<<24; on little-endian targets
	// (arm64/amd64) that's bytes [R,G,B,A], exactly NRGBA.Pix order. Alpha is a
	// constant 255, so NRGBA's non-premultiplied contract is satisfied.
	copy(img.Pix, rng.Bytes())
	return img, nil
}
