// camera.go: shared orbit-camera geometry. Both the SDF sphere tracer
// (raycast.go) and the triangle-mesh rasterizer (mesh.go) derive their view
// from cameraFor, so a meshed preview frames the model identically to the
// traced one.

package render

import (
	"math"

	"github.com/chewxy/math32"
	"github.com/soypat/geometry/ms3"
)

// viewCam is the resolved camera for one frame.
type viewCam struct {
	eye     ms3.Vec
	forward ms3.Vec // unit, eye -> lookAt
	right   ms3.Vec // unit
	up      ms3.Vec // unit
	fov     float32 // vertical field of view, radians
	diag    float32 // bounding-box diagonal (depth extents, step caps)
}

// cameraFor resolves the orbit camera for bounding box bb and user controls.
func cameraFor(bb ms3.Box, cp CameraParams) viewCam {
	center := bb.Center()
	size := bb.Size()
	diag := math32.Sqrt(size.X*size.X + size.Y*size.Y + size.Z*size.Z)
	if diag == 0 {
		diag = 1
	}

	forward := ms3.Unit(ms3.Vec{
		X: math32.Cos(cp.Elevation) * math32.Cos(cp.Azimuth),
		Y: math32.Cos(cp.Elevation) * math32.Sin(cp.Azimuth),
		Z: math32.Sin(cp.Elevation),
	})
	right := ms3.Unit(ms3.Vec{X: -math32.Sin(cp.Azimuth), Y: math32.Cos(cp.Azimuth), Z: 0})
	up := ms3.Unit(ms3.Cross(forward, right))

	zoom := cp.Zoom
	if zoom == 0 {
		zoom = 1
	}
	dist := diag * 1.5 * zoom
	pan := ms3.Add(ms3.Scale(cp.PanX*diag*0.1, right), ms3.Scale(cp.PanY*diag*0.1, up))
	lookAt := ms3.Add(center, pan)
	eye := ms3.Add(lookAt, ms3.Scale(dist, forward))

	// Re-derive an orthonormal basis from the final eye->lookAt direction,
	// matching the original two-step construction in RenderAngled.
	fwd := ms3.Unit(ms3.Sub(lookAt, eye))
	r := ms3.Unit(ms3.Cross(fwd, up))
	u := ms3.Cross(r, fwd)

	return viewCam{
		eye:     eye,
		forward: fwd,
		right:   r,
		up:      u,
		fov:     float32(math.Pi / 4),
		diag:    diag,
	}
}

// lightDir is the shared key-light direction for shading.
var lightDir = ms3.Unit(ms3.Vec{X: 1, Y: 1, Z: 1})
