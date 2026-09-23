// Package render implements fast SDF3 → SDF2 projection + rasterization
// for cadpad TUI previews. It uses CPU evaluation (gleval) + glrender's
// ImageRendererSDF2, with simple caching and modest default resolutions
// tuned for interactive LLM tool use (sub-100ms typical previews).
package render

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"sync"

	"github.com/NimbleMarkets/ds4go-apps/ntgpu"
	"github.com/chewxy/math32"
	"github.com/soypat/geometry/ms2"
	"github.com/soypat/geometry/ms3"
	"github.com/soypat/gsdf/gleval"
	"github.com/soypat/gsdf/glrender"
	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// Projection matches world.Projection but avoids import cycle for pure render pkg.
type Projection string

const (
	ProjXY    Projection = "xy"
	ProjXZ    Projection = "xz"
	ProjYZ    Projection = "yz"
	ProjAngle Projection = "angle"
)

// PreviewConfig tunes raster quality vs speed.
type PreviewConfig struct {
	// MaxEdge is the max pixels on the longer side of the output image.
	// Typical interactive value: 160-256. Larger slows preview.
	MaxEdge int
	// GPUMaxEdge caps the longer side of GPU raymarch output, which is cheap
	// enough to run at display resolution. Zero means twice MaxEdge. The CPU
	// paths always use MaxEdge.
	GPUMaxEdge int
	// EvalBuffer is passed to NewImageRendererSDF2 (min 4096 recommended).
	EvalBuffer int
	// ColorConv if non-nil overrides the default inside/outside scheme.
	ColorConv func(float32) color.Color
}

var DefaultPreviewConfig = PreviewConfig{
	MaxEdge:    768,
	GPUMaxEdge: 1536,
	EvalBuffer: 8192,
	ColorConv:  NiceColorConv,
}

// cachedSDF2 holds a CPU-wrapped 2D projection + its bounds for fast re-render.
type cachedSDF2 struct {
	sdf2 gleval.SDF2
	bb   ms2.Box
}

// projectSDF3ToSDF2 creates a 2D midplane slice of the 3D SDF for the given
// orthographic projection. This is a fast approximation good enough for
// LLM-driven iterative modeling (true ray-projected silhouettes would be slower).
func projectSDF3ToSDF2(s3 simplesdf.SDF3, p Projection) (gleval.SDF2, ms2.Box, error) {
	if s3.Shader() == nil {
		return nil, ms2.Box{}, errors.New("nil SDF3 shader")
	}
	// Wrap once for CPU eval. We keep the wrapper; caller may cache.
	s3cpu, err := gleval.NewCPUSDF3(s3.Shader())
	if err != nil {
		return nil, ms2.Box{}, fmt.Errorf("NewCPUSDF3: %w", err)
	}
	bb3 := s3cpu.Bounds()
	center := bb3.Center()

	var bb2 ms2.Box

	switch p {
	case ProjXY:
		bb2 = ms2.Box{Min: ms2.Vec{X: bb3.Min.X, Y: bb3.Min.Y}, Max: ms2.Vec{X: bb3.Max.X, Y: bb3.Max.Y}}
	case ProjXZ:
		bb2 = ms2.Box{Min: ms2.Vec{X: bb3.Min.X, Y: bb3.Min.Z}, Max: ms2.Vec{X: bb3.Max.X, Y: bb3.Max.Z}}
	case ProjYZ:
		bb2 = ms2.Box{Min: ms2.Vec{X: bb3.Min.Y, Y: bb3.Min.Z}, Max: ms2.Vec{X: bb3.Max.Y, Y: bb3.Max.Z}}
	default:
		return nil, ms2.Box{}, fmt.Errorf("unknown projection %q", p)
	}

	// Pad the 2D bounds so the rendered image shows margin around the object.
	padX := (bb2.Max.X - bb2.Min.X) * 0.15
	padY := (bb2.Max.Y - bb2.Min.Y) * 0.15
	bb2.Min.X -= padX
	bb2.Min.Y -= padY
	bb2.Max.X += padX
	bb2.Max.Y += padY

	var slice2d gleval.SDF2
	switch p {
	case ProjXY:
		slice2d = &axisSlice{s3: s3cpu, fixAxis: 2, fixVal: center.Z, bb2: bb2}
	case ProjXZ:
		slice2d = &axisSlice{s3: s3cpu, fixAxis: 1, fixVal: center.Y, bb2: bb2}
	case ProjYZ:
		slice2d = &axisSlice{s3: s3cpu, fixAxis: 0, fixVal: center.X, bb2: bb2}
	}
	return slice2d, bb2, nil
}

// axisSlice implements gleval.SDF2 by evaluating the parent SDF3 at a fixed
// coordinate on the omitted axis (midplane cross-section).
type axisSlice struct {
	s3      *gleval.SDF3CPU
	fixAxis int // 0=X, 1=Y, 2=Z
	fixVal  float32
	bb2     ms2.Box // padded 2D bounds for rendering
}

func (a *axisSlice) Bounds() ms2.Box {
	return a.bb2
}

func (a *axisSlice) Evaluate(pos []ms2.Vec, dist []float32, userData any) error {
	if len(pos) != len(dist) || len(pos) == 0 {
		return errors.New("pos/dist length mismatch or empty")
	}
	// Build 3D positions with the fixed axis.
	tmp := make([]ms3.Vec, len(pos))
	switch a.fixAxis {
	case 0: // fix X, vary YZ → pos.X=Y, pos.Y=Z
		for i, p := range pos {
			tmp[i] = ms3.Vec{X: a.fixVal, Y: p.X, Z: p.Y}
		}
	case 1: // fix Y, vary XZ
		for i, p := range pos {
			tmp[i] = ms3.Vec{X: p.X, Y: a.fixVal, Z: p.Y}
		}
	default: // fix Z, vary XY
		for i, p := range pos {
			tmp[i] = ms3.Vec{X: p.X, Y: p.Y, Z: a.fixVal}
		}
	}
	return a.s3.Evaluate(tmp, dist, userData)
}

// Renderer wraps a reusable glrender.ImageRendererSDF2 + small cache of
// projected SDF2s so repeated previews of the same object+proj are near-instant.
type Renderer struct {
	mu        sync.Mutex
	ir        *glrender.ImageRendererSDF2
	cfg       PreviewConfig
	cache     map[string]cachedSDF2 // key = name + ":" + proj
	lastSz    map[string]image.Rectangle
	meshCache map[string][]ms3.Triangle // key = object name (3D angle view)

	// gpuPipelines caches one compiled compute pipeline per object name for
	// the GPU raymarch path (RenderAngledGPU). The pipeline only depends on
	// the SDF/geometry (hashed via wgslHash), so interactive camera moves
	// reuse it instead of recompiling WGSL→MSL every frame. Entries hold live
	// GPU objects for the Renderer's lifetime; Invalidate/ClearCache Release
	// them on the shared GPU executor.
	gpuPipelines *ntgpu.PipelineCache[*gpuPipeline]
}

// NewRenderer constructs a renderer. A single Renderer should be shared by
// the TUI (and any headless preview path).
func NewRenderer(cfg PreviewConfig) (*Renderer, error) {
	if cfg.MaxEdge <= 0 {
		cfg.MaxEdge = DefaultPreviewConfig.MaxEdge
	}
	if cfg.GPUMaxEdge <= 0 {
		cfg.GPUMaxEdge = 2 * cfg.MaxEdge
	}
	if cfg.EvalBuffer < 4096 {
		cfg.EvalBuffer = DefaultPreviewConfig.EvalBuffer
	}
	ir, err := glrender.NewImageRendererSDF2(cfg.EvalBuffer, cfg.ColorConv)
	if err != nil {
		return nil, err
	}
	return &Renderer{
		ir:        ir,
		cfg:       cfg,
		cache:     make(map[string]cachedSDF2),
		lastSz:    make(map[string]image.Rectangle),
		meshCache: make(map[string][]ms3.Triangle),
		gpuPipelines: ntgpu.NewPipelineCache(gpuExecutor, func(p *gpuPipeline) error {
			p.Release()
			return nil
		}),
	}, nil
}

// RenderAngledMesh renders the 3D angle view by rasterizing a cached
// triangle mesh of the object. The expensive SDF march happens once per
// object (cached by name); subsequent camera moves only re-rasterize, so
// cost is independent of CSG-tree size. Call Invalidate(name) or
// ClearCache when the object's geometry changes. downscale < 1 means 1.
func (r *Renderer) RenderAngledMesh(s3 simplesdf.SDF3, name string, cp CameraParams, maxW, maxH, downscale int) (image.Image, image.Rectangle, error) {
	if s3.Shader() == nil {
		return nil, image.Rectangle{}, errors.New("nil SDF3")
	}
	r.mu.Lock()
	tris, ok := r.meshCache[name]
	r.mu.Unlock()

	if !ok {
		var err error
		tris, err = MeshObject(s3, 0)
		if err != nil {
			return nil, image.Rectangle{}, err
		}
		r.mu.Lock()
		r.meshCache[name] = tris
		r.mu.Unlock()
	}

	r.mu.Lock()
	maxEdge := r.cfg.MaxEdge
	r.mu.Unlock()
	return rasterizeMesh(tris, cp, maxW, maxH, maxEdge, downscale)
}

// Render produces an image.Image for the given object's projection.
// It returns the image, the actual pixel rect used, and any error.
// The image is always a paletted or RGBA with reasonable contrast for terminal.
func (r *Renderer) Render(s3 simplesdf.SDF3, name string, proj Projection, maxW, maxH int) (image.Image, image.Rectangle, error) {
	if s3.Shader() == nil {
		return nil, image.Rectangle{}, errors.New("nil SDF3")
	}
	key := name + ":" + string(proj)

	r.mu.Lock()
	c, ok := r.cache[key]
	r.mu.Unlock()

	var sdf2 gleval.SDF2
	var bb ms2.Box
	var err error
	if !ok {
		sdf2, bb, err = projectSDF3ToSDF2(s3, proj)
		if err != nil {
			return nil, image.Rectangle{}, err
		}
		r.mu.Lock()
		r.cache[key] = cachedSDF2{sdf2: sdf2, bb: bb}
		r.mu.Unlock()
	} else {
		sdf2 = c.sdf2
		bb = c.bb
	}

	// Compute target pixel size from bb aspect + caller max constraints.
	w, h := computeTargetSize(bb, maxW, maxH, r.cfg.MaxEdge)
	if w < 8 {
		w = 8
	}
	if h < 8 {
		h = 8
	}
	rect := image.Rect(0, 0, w, h)

	// Allocate an editable image (NRGBA is safe for Set).
	img := image.NewNRGBA(rect)

	// glrender expects an image that implements Set; NRGBA does.
	if err := r.ir.Render(sdf2, img, nil); err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("render sdf2: %w", err)
	}

	// Optional: invert Y so +Y points up in the picture (common for CAD top view).
	// The renderer already handles image coords; we can leave as-is for now.

	r.mu.Lock()
	r.lastSz[key] = rect
	r.mu.Unlock()
	return img, rect, nil
}

// computeTargetSize picks a pixel size <= maxEdge on long side, preserving
// the SDF bounding box aspect ratio, and respecting caller maxW/maxH.
func computeTargetSize(bb ms2.Box, maxW, maxH, maxEdge int) (int, int) {
	sz := bb.Size()
	if sz.X <= 0 || sz.Y <= 0 {
		return 64, 64
	}
	aspect := sz.X / sz.Y

	var pw, ph float64
	if aspect >= 1 {
		pw = float64(maxEdge)
		ph = pw / float64(aspect)
	} else {
		ph = float64(maxEdge)
		pw = ph * float64(aspect)
	}

	// Respect explicit pane maxes.
	if maxW > 0 && pw > float64(maxW) {
		pw = float64(maxW)
		ph = pw / float64(aspect)
	}
	if maxH > 0 && ph > float64(maxH) {
		ph = float64(maxH)
		pw = ph * float64(aspect)
	}

	// Clamp to sane minimums and integers.
	w := int(math.Max(8, math.Round(pw)))
	h := int(math.Max(8, math.Round(ph)))
	return w, h
}

// ClearCache drops all projected SDF2 wrappers (call on major world clear or
// after heavy transform that invalidates assumptions).
func (r *Renderer) ClearCache() {
	r.mu.Lock()
	r.cache = make(map[string]cachedSDF2)
	r.lastSz = make(map[string]image.Rectangle)
	r.meshCache = make(map[string][]ms3.Triangle)
	r.mu.Unlock()
	// GPU resources are removed from their cache before Release is queued on the
	// executor, so a concurrent render cannot pick a resource being freed.
	_ = r.gpuPipelines.Clear()
}

// SetMaxEdge updates the resolution cap and drops size cache.
func (r *Renderer) SetMaxEdge(edge int) {
	if edge < 64 {
		edge = 64
	}
	r.mu.Lock()
	r.cfg.MaxEdge = edge
	r.cfg.GPUMaxEdge = 2 * edge
	r.lastSz = make(map[string]image.Rectangle)
	r.mu.Unlock()
}

// Invalidate drops all cached projections for the given object name.
func (r *Renderer) Invalidate(name string) {
	r.mu.Lock()
	for _, proj := range []Projection{ProjXY, ProjXZ, ProjYZ} {
		key := name + ":" + string(proj)
		delete(r.cache, key)
		delete(r.lastSz, key)
	}
	delete(r.meshCache, name)
	r.mu.Unlock()
	// Release on the executor goroutine, serialized after any in-flight dispatch.
	_ = r.gpuPipelines.Invalidate(name)
}

// DefaultColorConv returns a simple high-contrast scheme (black=inside).
// Matches the default inside glrender when nil conv is passed.
func DefaultColorConv(d float32) color.Color {
	switch {
	case math32.IsNaN(d) || math32.IsInf(d, 0):
		return color.RGBA{R: 255, A: 255}
	case d > 0:
		return color.White
	default:
		return color.Black
	}
}

// NiceColorConv is a CAD-like scheme with teal inside, warm outside,
// and a bright white edge highlight so boundaries are crisp.
func NiceColorConv(d float32) color.Color {
	if math32.IsNaN(d) || math32.IsInf(d, 0) {
		return color.RGBA{R: 255, A: 255}
	}
	var c ms3.Vec
	if d > 0 {
		c = ms3.Vec{X: 0.90, Y: 0.60, Z: 0.30} // outside: warm amber
	} else {
		c = ms3.Vec{X: 0.25, Y: 0.70, Z: 0.80} // inside: teal
	}
	// Distance-based falloff (darker farther from surface).
	c = ms3.Scale(1-math32.Exp(-6*math32.Abs(d)), c)
	// Subtle cosine bands for texture.
	c = ms3.Scale(0.8+0.2*math32.Cos(150*d), c)
	// White edge highlight at the zero-crossing.
	edge := 1 - smoothstep(0, 0.02, math32.Abs(d))
	c = ms3.InterpElem(c, ms3.Vec{X: 1, Y: 1, Z: 1}, ms3.Vec{X: edge, Y: edge, Z: edge})
	return color.RGBA{
		R: uint8(clamp01(c.X) * 255),
		G: uint8(clamp01(c.Y) * 255),
		B: uint8(clamp01(c.Z) * 255),
		A: 255,
	}
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func smoothstep(edge0, edge1, x float32) float32 {
	t := clamp01((x - edge0) / (edge1 - edge0))
	return t * t * (3 - 2*t)
}
