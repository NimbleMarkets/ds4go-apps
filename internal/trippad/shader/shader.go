// Package shader assembles WGSL and caches GPU compute pipelines on one executor.
package shader

import (
	"crypto/sha256"
	"fmt"
	"image"
	"strings"
	"sync"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/ntgpu"
	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
)

type Source struct {
	Name      string         `json:"name"`
	ShadeBody string         `json:"shade_body"`
	Params    []params.Param `json:"params"`
}

// tripUniform is 96 bytes. Four vec4 slots avoid uniform-array scalar stride
// differences: params begin at byte 32, with 16 tightly packed floats.
type tripUniform struct {
	Time, Dt float32
	W, H     uint32
	Frame    uint32
	_pad     [3]uint32
	Params   [16]float32
}

const kernelTemplate = `
struct Uniforms {
 time: f32, dt: f32, w: u32, h: u32,
 frame: u32, pad0: u32, pad1: u32, pad2: u32,
 params: array<vec4<f32>, 4>,
}
@group(0) @binding(0) var<uniform> uni: Uniforms;
@group(0) @binding(1) var<storage, read_write> pixels: array<u32>;
fn hash(p: vec2<f32>) -> f32 { return fract(sin(dot(p, vec2<f32>(127.1,311.7))) * 43758.5453); }
fn noise(p: vec2<f32>) -> f32 {
 let i = floor(p); let f = fract(p); let u = f*f*(3.0 - 2.0*f);
 return mix(mix(hash(i),hash(i+vec2<f32>(1.0,0.0)),u.x), mix(hash(i+vec2<f32>(0.0,1.0)),hash(i+vec2<f32>(1.0,1.0)),u.x),u.y);
}
fn fbm(p: vec2<f32>) -> f32 {
 var q = p; var v = 0.0; var a = 0.5;
 for(var i = 0; i < 5; i = i+1) { v = v+a*noise(q); q=q*2.03+vec2<f32>(7.1,3.8); a=a*0.5; }
 return v;
}
fn palette(t: f32) -> vec3<f32> { return 0.5+0.5*cos(6.2831853*(vec3<f32>(t)+vec3<f32>(0.0,0.33,0.67))); }
fn rot2d(a: f32) -> mat2x2<f32> { let c=cos(a); let s=sin(a); return mat2x2<f32>(c,s,-s,c); }
%ACCESSORS%
fn shade(uv: vec2<f32>) -> vec3<f32> {
%SHADE%
}
fn pack(c: vec3<f32>) -> u32 {
 let rgb = vec3<u32>(clamp(c,vec3<f32>(0.0),vec3<f32>(1.0))*255.0);
 return rgb.x | (rgb.y << 8u) | (rgb.z << 16u) | (255u << 24u);
}
@compute @workgroup_size(8,8,1)
fn main(@builtin(global_invocation_id) gid: vec3<u32>) {
 if(gid.x >= uni.w || gid.y >= uni.h) { return; }
 let uv = (vec2<f32>(gid.xy)+vec2<f32>(0.5)-vec2<f32>(f32(uni.w),f32(uni.h))*0.5)/f32(uni.h)*2.0;
 pixels[gid.y*uni.w+gid.x] = pack(shade(uv));
}
`

func BuildWGSL(src Source) (string, error) {
	doc, err := BuildDocument(src)
	return doc.Code, err
}

// Document maps the model's editable body into the complete GPU module.
// Positions are 1-based; columns are unchanged because the body is not indented.
type Document struct {
	Code               string
	BodyStart, BodyEnd int
}

func BuildDocument(src Source) (Document, error) {
	if err := params.Validate(src.Params); err != nil {
		return Document{}, err
	}
	if strings.TrimSpace(src.ShadeBody) == "" {
		return Document{}, fmt.Errorf("shade body required")
	}
	var access strings.Builder
	for i, p := range src.Params {
		fmt.Fprintf(&access, "fn p_%s() -> f32 { return uni.params[%d][%d]; }\n", p.Name, i/4, i%4)
	}
	prefix, suffix, _ := strings.Cut(kernelTemplate, "%SHADE%")
	prefix = strings.ReplaceAll(prefix, "%ACCESSORS%", access.String())
	start := strings.Count(prefix, "\n") + 1
	return Document{Code: prefix + src.ShadeBody + suffix, BodyStart: start, BodyEnd: start + strings.Count(src.ShadeBody, "\n")}, nil
}

type compiled struct {
	shader *wgpu.ShaderModule
	bgl    *wgpu.BindGroupLayout
	layout *wgpu.PipelineLayout
	pipe   *wgpu.ComputePipeline
}

func (p *compiled) release() {
	if p.pipe != nil {
		p.pipe.Release()
	}
	if p.layout != nil {
		p.layout.Release()
	}
	if p.bgl != nil {
		p.bgl.Release()
	}
	if p.shader != nil {
		p.shader.Release()
	}
}
func build(dev *wgpu.Device, code string) (_ *compiled, err error) {
	p := &compiled{}
	defer func() {
		if err != nil {
			p.release()
		}
	}()
	p.shader, err = dev.CreateShaderModule(&wgpu.ShaderModuleDescriptor{Label: "trippad", WGSL: code})
	if err != nil {
		return nil, err
	} // Preserve naga diagnostics verbatim.
	p.bgl, err = dev.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{Entries: []gputypes.BindGroupLayoutEntry{
		{Binding: 0, Visibility: wgpu.ShaderStageCompute, Buffer: &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeUniform}},
		{Binding: 1, Visibility: wgpu.ShaderStageCompute, Buffer: &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeStorage}},
	}})
	if err != nil {
		return nil, err
	}
	p.layout, err = dev.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{BindGroupLayouts: []*wgpu.BindGroupLayout{p.bgl}})
	if err != nil {
		return nil, err
	}
	p.pipe, err = dev.CreateComputePipeline(&wgpu.ComputePipelineDescriptor{Layout: p.layout, Module: p.shader, EntryPoint: "main"})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Pipeline owns a bounded cache. GPU resources are created, dispatched and
// released only inside exec.Do. Close waits for current operations.
type Pipeline struct {
	mu     sync.Mutex
	exec   *ntgpu.Executor
	cache  map[[32]byte]*compiled
	closed bool
}

func NewPipeline(exec *ntgpu.Executor) *Pipeline {
	if exec == nil {
		exec = ntgpu.DefaultExecutor()
	}
	return &Pipeline{exec: exec, cache: make(map[[32]byte]*compiled)}
}
func (p *Pipeline) lookup(dev *wgpu.Device, code string) (*compiled, error) {
	key := sha256.Sum256([]byte(code))
	if c := p.cache[key]; c != nil {
		return c, nil
	}
	c, err := build(dev, code)
	if err != nil {
		return nil, err
	}
	if len(p.cache) >= 8 {
		for k, old := range p.cache {
			old.release()
			delete(p.cache, k)
			break
		}
	}
	p.cache[key] = c
	return c, nil
}
func (p *Pipeline) Compile(src Source) error {
	code, err := BuildWGSL(src)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("pipeline closed")
	}
	return p.exec.Do(func(dev *wgpu.Device) error { _, err := p.lookup(dev, code); return err })
}
func (p *Pipeline) Render(src Source, values [16]float32, t, dt float32, frame uint32, w, h int) (*image.NRGBA, error) {
	if w < 1 || h < 1 || w > 4096 || h > 4096 {
		return nil, fmt.Errorf("render dimensions must be 1..4096")
	}
	code, err := BuildWGSL(src)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("pipeline closed")
	}
	var img *image.NRGBA
	err = p.exec.Do(func(dev *wgpu.Device) error {
		c, err := p.lookup(dev, code)
		if err != nil {
			return err
		}
		u := tripUniform{Time: t, Dt: dt, W: uint32(w), H: uint32(h), Frame: frame, Params: values}
		img, err = ntgpu.DispatchRGBA8(dev, ntgpu.ImageDispatch{Label: "trippad", Width: w, Height: h, WorkgroupX: 8, WorkgroupY: 8, Uniform: ntgpu.BytesOf(&u), BindGroupLayout: c.bgl, Pipeline: c.pipe, UniformBinding: 0, OutputBinding: 1})
		return err
	})
	return img, err
}
func (p *Pipeline) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if len(p.cache) == 0 {
		return nil
	}
	return p.exec.Do(func(*wgpu.Device) error {
		for k, c := range p.cache {
			c.release()
			delete(p.cache, k)
		}
		return nil
	})
}
