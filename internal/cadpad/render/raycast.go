package render

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"

	"github.com/chewxy/math32"
	"github.com/soypat/geometry/ms3"
	"github.com/soypat/gsdf/gleval"
	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// Camera describes a simple perspective camera.
// CameraParams holds user-controllable orbit camera settings.
type CameraParams struct {
	Azimuth   float32 // radians around Z, 0 = +X
	Elevation float32 // radians above horizontal, 0 = level, π/2 = straight up
	Zoom      float32 // distance multiplier, 1.0 = default
	PanX      float32 // pan right (units of 0.1×object diagonal)
	PanY      float32 // pan up    (units of 0.1×object diagonal)
}

// activeRay tracks one pixel's ray during batched sphere tracing.
type activeRay struct {
	origin ms3.Vec
	dir    ms3.Vec
	t      float32
	pix    int
}

// RenderAngled renders a perspective preview of s3 from a user-controllable orbit camera.
// It uses CPU sphere tracing + simple Lambertian shading.
func (r *Renderer) RenderAngled(s3 simplesdf.SDF3, name string, cp CameraParams, maxW, maxH int) (image.Image, image.Rectangle, error) {
	return r.RenderAngledScale(s3, name, cp, maxW, maxH, 1)
}

// RenderAngledScale renders like RenderAngled at 1/downscale of the
// resolution RenderAngled would pick (after both the pane budget and the
// MaxEdge cap are applied). Sphere tracing cost scales with pixel count, so
// downscale 3 is ~9x faster — used for interactive camera movement, with a
// full-resolution pass once input settles. downscale values < 1 mean 1.
func (r *Renderer) RenderAngledScale(s3 simplesdf.SDF3, name string, cp CameraParams, maxW, maxH, downscale int) (image.Image, image.Rectangle, error) {
	if s3.Shader() == nil {
		return nil, image.Rectangle{}, errors.New("nil SDF3 shader")
	}

	w, h := computeTargetSizeAngled(maxW, maxH, r.cfg.MaxEdge)
	if downscale > 1 {
		w = max(8, w/downscale)
		h = max(8, h/downscale)
	}
	rect := image.Rect(0, 0, w, h)
	img := image.NewNRGBA(rect)

	// Pre-fill background.
	for i := 0; i < w*h; i++ {
		off := i * 4
		img.Pix[off+0] = 25
		img.Pix[off+1] = 28
		img.Pix[off+2] = 35
		img.Pix[off+3] = 255
	}

	// Build camera basis from azimuth/elevation (shared with the mesh
	// rasterizer so both framings match).
	bb3cpu, err := gleval.NewCPUSDF3(s3.Shader())
	if err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("NewCPUSDF3 bounds check: %w", err)
	}
	bb := bb3cpu.Bounds()
	cam := cameraFor(bb, cp)
	eye := cam.eye
	forward := cam.forward
	right := cam.right
	up := cam.up
	diag := cam.diag
	aspect := float32(w) / float32(h)
	tanFOV := math32.Tan(cam.fov / 2)

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers < 1 {
		numWorkers = 1
	}

	var wg sync.WaitGroup
	errs := make([]error, numWorkers)

	for wid := 0; wid < numWorkers; wid++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			s3cpu, err := gleval.NewCPUSDF3(s3.Shader())
			if err != nil {
				errs[workerID] = err
				return
			}

			yStart := workerID * h / numWorkers
			yEnd := (workerID + 1) * h / numWorkers
			if yStart >= yEnd {
				return
			}

			chunkSize := (yEnd - yStart) * w
			active := make([]activeRay, 0, chunkSize)
			for y := yStart; y < yEnd; y++ {
				v := (1.0 - 2.0*(float32(y)+0.5)/float32(h)) * tanFOV
				for x := 0; x < w; x++ {
					u := (2.0*(float32(x)+0.5)/float32(w) - 1.0) * aspect * tanFOV
					d := ms3.Unit(ms3.Add(ms3.Add(forward, ms3.Scale(u, right)), ms3.Scale(v, up)))
					active = append(active, activeRay{
						origin: eye,
						dir:    d,
						t:      0,
						pix:    y*w + x,
					})
				}
			}

			const maxSteps = 80
			const eps = 0.002
			maxT := diag * 3

			next := make([]activeRay, 0, len(active))
			posBuf := make([]ms3.Vec, len(active))
			distBuf := make([]float32, len(active))

			for step := 0; step < maxSteps && len(active) > 0; step++ {
				for i, r := range active {
					posBuf[i] = ms3.Add(r.origin, ms3.Scale(r.t, r.dir))
				}

				if err := s3cpu.Evaluate(posBuf[:len(active)], distBuf[:len(active)], nil); err != nil {
					errs[workerID] = err
					return
				}

				next = next[:0]
				for i := range active {
					r := &active[i]
					d := distBuf[i]
					if math32.IsNaN(d) || math32.IsInf(d, 0) {
						continue
					}
					if d < eps {
						// Hit — shade pixel.
						n := normal(s3cpu, posBuf[i])
						diffuse := maxFloat32(0.0, ms3.Dot(n, lightDir))
						shade := 0.25 + diffuse*0.75
						if shade > 1 {
							shade = 1
						}
						c := uint8(shade * 255)
						off := r.pix * 4
						img.Pix[off+0] = uint8(float32(c) * 0.85) // slight teal tint
						img.Pix[off+1] = uint8(float32(c) * 0.95)
						img.Pix[off+2] = c
						img.Pix[off+3] = 255
						continue
					}
					r.t += d * 0.95
					if r.t < maxT {
						next = append(next, *r)
					}
				}
				active, next = next, active
			}
		}(wid)
	}

	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, image.Rectangle{}, err
		}
	}

	return img, rect, nil
}

func normal(s3 *gleval.SDF3CPU, p ms3.Vec) ms3.Vec {
	eps := float32(0.001)
	dx := evalDist(s3, ms3.Add(p, ms3.Vec{X: eps, Y: 0, Z: 0}))
	dy := evalDist(s3, ms3.Add(p, ms3.Vec{X: 0, Y: eps, Z: 0}))
	dz := evalDist(s3, ms3.Add(p, ms3.Vec{X: 0, Y: 0, Z: eps}))
	cx := evalDist(s3, ms3.Add(p, ms3.Vec{X: -eps, Y: 0, Z: 0}))
	cy := evalDist(s3, ms3.Add(p, ms3.Vec{X: 0, Y: -eps, Z: 0}))
	cz := evalDist(s3, ms3.Add(p, ms3.Vec{X: 0, Y: 0, Z: -eps}))
	return ms3.Unit(ms3.Vec{X: dx - cx, Y: dy - cy, Z: dz - cz})
}

func evalDist(s3 *gleval.SDF3CPU, p ms3.Vec) float32 {
	pos := []ms3.Vec{p}
	dist := []float32{0}
	s3.Evaluate(pos, dist, nil)
	return dist[0]
}

func computeTargetSizeAngled(maxW, maxH, maxEdge int) (int, int) {
	if maxW <= 0 {
		maxW = maxEdge
	}
	if maxH <= 0 {
		maxH = maxEdge
	}
	w, h := maxW, maxH
	if w > h && w > maxEdge {
		h = h * maxEdge / w
		w = maxEdge
	} else if h >= w && h > maxEdge {
		w = w * maxEdge / h
		h = maxEdge
	}
	if w < 8 {
		w = 8
	}
	if h < 8 {
		h = 8
	}
	return w, h
}

func maxFloat32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
