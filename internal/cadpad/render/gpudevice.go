package render

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
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

var (
	gpuOnce sync.Once
	gpuDev  *wgpu.Device
	gpuErr  error
)

// device lazily creates a single gogpu device backed by a HARDWARE adapter.
// It returns an error (never a software-backend device) when no GPU is present.
func device() (*wgpu.Device, error) {
	gpuOnce.Do(func() {
		runtime.LockOSThread()
		inst, err := wgpu.CreateInstance(nil)
		if err != nil {
			gpuErr = fmt.Errorf("create instance: %w", err)
			return
		}
		adapter, err := inst.RequestAdapter(&wgpu.RequestAdapterOptions{
			PowerPreference: wgpu.PowerPreferenceHighPerformance,
			// Never request a software fallback adapter.
			ForceFallbackAdapter: false,
		})
		if err != nil {
			gpuErr = fmt.Errorf("request adapter: %w", err)
			return
		}
		if adapter == nil {
			gpuErr = errors.New("no GPU adapter")
			return
		}
		// Reject the software backend: we want a real GPU, not CPU emulation.
		if isSoftwareAdapter(adapter) {
			gpuErr = errors.New("only software adapter available")
			return
		}
		dev, err := adapter.RequestDevice(nil)
		if err != nil {
			gpuErr = fmt.Errorf("request device: %w", err)
			return
		}
		gpuDev = dev
	})
	return gpuDev, gpuErr
}

// isSoftwareAdapter reports whether the adapter is gogpu's CPU/software backend.
// gogpu exposes AdapterInfo.DeviceType and AdapterInfo.Backend; a software
// adapter reports DeviceTypeCPU and/or the empty (noop/software) backend.
func isSoftwareAdapter(a *wgpu.Adapter) bool {
	info := a.Info()
	return info.DeviceType == gputypes.DeviceTypeCPU || info.Backend == gputypes.BackendEmpty
}

func gpuAvailable() bool {
	d, err := device()
	return d != nil && err == nil
}

func requireGPU(t *testing.T) {
	t.Helper()
	if !gpuAvailable() {
		t.Skip("no GPU adapter; skipping GPU-execution test")
	}
}

// runComputeDouble runs computeDoubleWGSL over in and returns the result.
//
// It uploads in to a storage buffer, dispatches the doubling kernel, copies the
// result into a MapRead staging buffer, and maps that for readback. (Storage
// buffers are not directly mappable, so a CopyBufferToBuffer staging step is
// required before Finish.)
func runComputeDouble(in []float32) ([]float32, error) {
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
