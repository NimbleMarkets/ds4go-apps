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

// dispatchKernel runs the given raymarch WGSL over a w*h grid with the supplied
// camera uniform and returns the rgba8 result as an image.NRGBA.
//
// The output is an array<u32> storage buffer (one packed rgba8 per pixel).
// Storage buffers are not directly mappable, so the result is copied into a
// MapRead staging buffer (CopyBufferToBuffer before Finish) and that is mapped
// for readback — exactly as runComputeDouble does in gpudevice.go.
func dispatchKernel(wgsl string, cam gpuCam, w, h int) (*image.NRGBA, error) {
	dev, err := device()
	if err != nil {
		return nil, err
	}
	q := dev.Queue()
	npix := w * h
	outBytes := uint64(npix * 4)

	shader, err := dev.CreateShaderModule(&wgpu.ShaderModuleDescriptor{Label: "ray", WGSL: wgsl})
	if err != nil {
		return nil, fmt.Errorf("create shader: %w", err)
	}
	defer shader.Release()

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

	bgl, err := dev.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
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
		return nil, fmt.Errorf("create bind group layout: %w", err)
	}
	defer bgl.Release()

	bg, err := dev.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Layout: bgl,
		Entries: []wgpu.BindGroupEntry{
			{Binding: 0, Buffer: uni, Size: uint64(len(camBytes))},
			{Binding: 1, Buffer: out, Size: outBytes},
		},
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
	copy(img.Pix, rng.Bytes()) // rgba8 packed little-endian == NRGBA byte order
	return img, nil
}
