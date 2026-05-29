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
	ProjXY Projection = "xy"
	ProjXZ Projection = "xz"
	ProjYZ Projection = "yz"
)

// PreviewConfig tunes raster quality vs speed.
type PreviewConfig struct {
	// MaxEdge is the max pixels on the longer side of the output image.
	// Typical interactive value: 160-256. Larger slows preview.
	MaxEdge int
	// EvalBuffer is passed to NewImageRendererSDF2 (min 4096 recommended).
	EvalBuffer int
	// ColorConv if non-nil overrides the default inside/outside scheme.
	ColorConv func(float32) color.Color
}

var DefaultPreviewConfig = PreviewConfig{
	MaxEdge:    192,
	EvalBuffer: 8192,
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

	var slice2d gleval.SDF2
	var bb2 ms2.Box

	switch p {
	case ProjXY:
		// XY plane at Z = center.Z (top-down view)
		slice2d = &axisSlice{s3: s3cpu, fixAxis: 2, fixVal: center.Z, bb3: bb3}
		bb2 = ms2.Box{
			Min: ms2.Vec{X: bb3.Min.X, Y: bb3.Min.Y},
			Max: ms2.Vec{X: bb3.Max.X, Y: bb3.Max.Y},
		}
	case ProjXZ:
		// XZ plane at Y = center.Y
		slice2d = &axisSlice{s3: s3cpu, fixAxis: 1, fixVal: center.Y, bb3: bb3}
		bb2 = ms2.Box{
			Min: ms2.Vec{X: bb3.Min.X, Y: bb3.Min.Z},
			Max: ms2.Vec{X: bb3.Max.X, Y: bb3.Max.Z},
		}
	case ProjYZ:
		// YZ plane at X = center.X
		slice2d = &axisSlice{s3: s3cpu, fixAxis: 0, fixVal: center.X, bb3: bb3}
		bb2 = ms2.Box{
			Min: ms2.Vec{X: bb3.Min.Y, Y: bb3.Min.Z},
			Max: ms2.Vec{X: bb3.Max.Y, Y: bb3.Max.Z},
		}
	default:
		return nil, ms2.Box{}, fmt.Errorf("unknown projection %q", p)
	}
	return slice2d, bb2, nil
}

// axisSlice implements gleval.SDF2 by evaluating the parent SDF3 at a fixed
// coordinate on the omitted axis (midplane cross-section).
type axisSlice struct {
	s3      *gleval.SDF3CPU
	fixAxis int     // 0=X, 1=Y, 2=Z
	fixVal  float32
	bb3     ms3.Box
}

func (a *axisSlice) Bounds() ms2.Box {
	switch a.fixAxis {
	case 0: // YZ
		return ms2.Box{Min: ms2.Vec{X: a.bb3.Min.Y, Y: a.bb3.Min.Z}, Max: ms2.Vec{X: a.bb3.Max.Y, Y: a.bb3.Max.Z}}
	case 1: // XZ
		return ms2.Box{Min: ms2.Vec{X: a.bb3.Min.X, Y: a.bb3.Min.Z}, Max: ms2.Vec{X: a.bb3.Max.X, Y: a.bb3.Max.Z}}
	default: // XY
		return ms2.Box{Min: ms2.Vec{X: a.bb3.Min.X, Y: a.bb3.Min.Y}, Max: ms2.Vec{X: a.bb3.Max.X, Y: a.bb3.Max.Y}}
	}
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
	mu     sync.Mutex
	ir     *glrender.ImageRendererSDF2
	cfg    PreviewConfig
	cache  map[string]cachedSDF2 // key = name + ":" + proj
	lastSz map[string]image.Rectangle
}

// NewRenderer constructs a renderer. A single Renderer should be shared by
// the TUI (and any headless preview path).
func NewRenderer(cfg PreviewConfig) (*Renderer, error) {
	if cfg.MaxEdge <= 0 {
		cfg.MaxEdge = DefaultPreviewConfig.MaxEdge
	}
	if cfg.EvalBuffer < 4096 {
		cfg.EvalBuffer = DefaultPreviewConfig.EvalBuffer
	}
	ir, err := glrender.NewImageRendererSDF2(cfg.EvalBuffer, cfg.ColorConv)
	if err != nil {
		return nil, err
	}
	return &Renderer{
		ir:     ir,
		cfg:    cfg,
		cache:  make(map[string]cachedSDF2),
		lastSz: make(map[string]image.Rectangle),
	}, nil
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
	r.mu.Unlock()
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

// NiceColorConv is a more CAD-like scheme with teal inside, warm outside.
func NiceColorConv(d float32) color.Color {
	if math32.IsNaN(d) || math32.IsInf(d, 0) {
		return color.RGBA{R: 255, A: 255}
	}
	// Simple two-tone with slight shading by distance magnitude.
	if d <= 0 {
		// inside: dark teal
		v := uint8(40 + uint8(math32.Min(80, -d*12)))
		return color.RGBA{R: 20, G: 120 + v/3, B: 140, A: 255}
	}
	// outside: warm gray/orange tint
	v := uint8(math32.Min(200, d*25))
	return color.RGBA{R: 180 + v/4, G: 160 + v/5, B: 140, A: 255}
}
