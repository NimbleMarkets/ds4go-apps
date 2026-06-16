package render

import (
	"context"
	"fmt"
	"testing"
	"unsafe"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
	_ "github.com/gogpu/wgpu/hal/allbackends" // register Metal/Vulkan/etc. backends
)

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

// gpuDev / gpuErr hold the single gogpu device (or the failure to create one).
// They are created ONCE, on the executor goroutine, by gpuExecStart (see
// gpuexec.go) and read-only thereafter, so no further synchronization is needed
// for reads that happen on the executor thread or after gpuExecReady is closed.
var (
	gpuDev *wgpu.Device
	gpuErr error
)

// device returns the single gogpu device (or the init error). It is valid to
// call ONLY from within a gpuDo closure on the executor goroutine, where the
// device has already been created by gpuExecStart. It performs no creation of
// its own — that lives on the executor thread (createDevice in gpuexec.go) so
// every gogpu call, including device creation, runs on one stable OS thread.
func device() (*wgpu.Device, error) {
	return gpuDev, gpuErr
}

// isSoftwareAdapter reports whether the adapter is gogpu's CPU/software backend.
// gogpu exposes AdapterInfo.DeviceType and AdapterInfo.Backend; a software
// adapter reports DeviceTypeCPU and/or the empty (noop/software) backend.
func isSoftwareAdapter(a *wgpu.Adapter) bool {
	info := a.Info()
	return info.DeviceType == gputypes.DeviceTypeCPU || info.Backend == gputypes.BackendEmpty
}

// gpuAvailable reports whether a hardware GPU device was created. It triggers
// (and waits for) device init on the executor goroutine via gpuDo, then checks
// the resulting device/error. When there is no GPU, createDevice returns an
// error and this returns false promptly — it never blocks forever.
func gpuAvailable() bool {
	var ok bool
	// The closure runs on the executor thread, after device init has completed
	// (gpuDo waits on gpuExecReady first). It only reads the device, no GPU API
	// calls, so it cannot itself fail.
	_ = gpuDo(func() error {
		ok = gpuDev != nil && gpuErr == nil
		return nil
	})
	return ok
}

func requireGPU(t *testing.T) {
	t.Helper()
	if !gpuAvailable() {
		t.Skip("no GPU adapter; skipping GPU-execution test")
	}
}

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
		Entries: []wgpu.BindGroupLayoutEntry{{
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
