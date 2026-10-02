package render

import (
	"context"
	"fmt"
	"image"
	"unsafe"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
)

// This file holds GPU "spike" scaffolding that is referenced only by tests:
// the original compute round-trip (runComputeDouble + helpers) that proved the
// gogpu device works, the throwaway-pipeline dispatchKernel, and the
// transpiler compile-gate (compileKernel). None of it is reachable from the
// production render path (RenderAngledGPU uses buildGPUPipeline +
// dispatchPipeline), so it lives in a _test.go file to keep it out of the
// production binary.

// computeDoubleWGSL doubles each f32 in a storage buffer in place.
// Used by runComputeDouble as the spike's GPU round-trip kernel.
const computeDoubleWGSL = `
@group(0) @binding(0) var<storage, read_write> data: array<f32>;
@compute @workgroup_size(64)
fn main(@builtin(global_invocation_id) gid: vec3<u32>) {
    let i = gid.x;
    if (i >= arrayLength(&data)) { return; }
    data[i] = data[i] * 2.0;
}`

// runComputeDouble runs computeDoubleWGSL over in and returns the result. The
// GPU work is funneled onto the executor goroutine via gpuDo so it shares the
// single GPU thread with all other gogpu calls.
func runComputeDouble(in []float32) ([]float32, error) {
	if len(in) == 0 {
		return nil, nil // nothing to dispatch; avoids zero-sized buffers and &in[0] panic
	}
	var out []float32
	err := gpuDo(func() error {
		var derr error
		out, derr = runComputeDoubleOnExec(in)
		return derr
	})
	return out, err
}

// runComputeDoubleOnExec is the raw GPU body of runComputeDouble. It calls
// device() and uses gogpu objects directly, so it MUST run on the executor
// goroutine (i.e. only from inside a gpuDo closure); never call it via gpuDo
// again (that would deadlock).
//
// It uploads in to a storage buffer, dispatches the doubling kernel, copies the
// result into a MapRead staging buffer, and maps that for readback. (Storage
// buffers are not directly mappable, so a CopyBufferToBuffer staging step is
// required before Finish.)
func runComputeDoubleOnExec(in []float32) ([]float32, error) {
	dev, err := device()
	if err != nil {
		return nil, err
	}
	q := dev.Queue()
	byteLen := uint64(len(in) * 4)

	shader, err := dev.CreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label: "double", WGSL: computeDoubleWGSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create shader: %w", err)
	}
	defer shader.Release()

	storage, err := dev.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "data", Size: byteLen,
		Usage: wgpu.BufferUsageStorage | wgpu.BufferUsageCopySrc | wgpu.BufferUsageCopyDst,
	})
	if err != nil {
		return nil, fmt.Errorf("create storage buffer: %w", err)
	}
	defer storage.Release()

	staging, err := dev.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "staging", Size: byteLen,
		Usage: wgpu.BufferUsageMapRead | wgpu.BufferUsageCopyDst,
	})
	if err != nil {
		return nil, fmt.Errorf("create staging buffer: %w", err)
	}
	defer staging.Release()

	if err := q.WriteBuffer(storage, 0, float32sToBytes(in)); err != nil {
		return nil, fmt.Errorf("write buffer: %w", err)
	}

	bgl, err := dev.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Entries: []gputypes.BindGroupLayoutEntry{{
			Binding:    0,
			Visibility: wgpu.ShaderStageCompute,
			Buffer:     &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeStorage},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("create bind group layout: %w", err)
	}
	defer bgl.Release()

	bg, err := dev.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Layout: bgl,
		Entries: []wgpu.BindGroupEntry{{
			Binding: 0, Buffer: storage, Size: byteLen,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("create bind group: %w", err)
	}
	defer bg.Release()

	pl, err := dev.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		BindGroupLayouts: []*wgpu.BindGroupLayout{bgl},
	})
	if err != nil {
		return nil, fmt.Errorf("create pipeline layout: %w", err)
	}
	defer pl.Release()

	pipe, err := dev.CreateComputePipeline(&wgpu.ComputePipelineDescriptor{
		Layout: pl, Module: shader, EntryPoint: "main",
	})
	if err != nil {
		return nil, fmt.Errorf("create compute pipeline: %w", err)
	}
	defer pipe.Release()

	enc, err := dev.CreateCommandEncoder(nil)
	if err != nil {
		return nil, fmt.Errorf("create command encoder: %w", err)
	}
	pass, err := enc.BeginComputePass(nil)
	if err != nil {
		return nil, fmt.Errorf("begin compute pass: %w", err)
	}
	pass.SetPipeline(pipe)
	pass.SetBindGroup(0, bg, nil)
	pass.Dispatch((uint32(len(in))+63)/64, 1, 1)
	if err := pass.End(); err != nil {
		return nil, fmt.Errorf("end compute pass: %w", err)
	}
	enc.CopyBufferToBuffer(storage, 0, staging, 0, byteLen)
	cmd, err := enc.Finish()
	if err != nil {
		return nil, fmt.Errorf("finish: %w", err)
	}
	if _, err := q.Submit(cmd); err != nil {
		return nil, fmt.Errorf("submit: %w", err)
	}

	if err := staging.Map(context.Background(), wgpu.MapModeRead, 0, byteLen); err != nil {
		return nil, fmt.Errorf("map: %w", err)
	}
	defer staging.Unmap()
	rng, err := staging.MappedRange(0, byteLen)
	if err != nil {
		return nil, fmt.Errorf("mapped range: %w", err)
	}
	defer rng.Release()
	return bytesToFloat32s(rng.Bytes(), len(in)), nil
}

func float32sToBytes(f []float32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(&f[0])), len(f)*4)
}

func bytesToFloat32s(b []byte, n int) []float32 {
	out := make([]float32, n)
	copy(out, unsafe.Slice((*float32)(unsafe.Pointer(&b[0])), n))
	return out
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
	// Use the raymarch kernel's real bindings. A nil layout happened to work
	// on Metal, but Vulkan requires the uniform/storage descriptor layout and
	// the NVIDIA driver may crash when the shader accesses missing bindings.
	p, err := buildGPUPipeline(wgsl)
	if err != nil {
		return err
	}
	p.Release()
	return nil
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
