// mesh.go: triangle-mesh path for the 3D preview. Marching the SDF to
// triangles is the same expensive octree pass STL export uses, so it runs
// once per geometry edit (cached by the Renderer); rasterizing the cached
// mesh on each camera move is independent of CSG-tree size and turns a
// "seconds per frame" scene into a "milliseconds per frame" one.

package render

import (
	"errors"
	"fmt"
	"image"

	"github.com/chewxy/math32"
	"github.com/soypat/geometry/ms3"
	"github.com/soypat/gsdf/gleval"
	"github.com/soypat/gsdf/glrender"
	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// defaultMeshDivisions controls preview mesh resolution: the octree cube
// edge is bbox-diagonal / divisions. Coarser than STL export (256-512) so
// the one-time march stays interactive.
const defaultMeshDivisions = 96

// MeshObject marches an SDF to a triangle mesh via gsdf's octree renderer
// (the same path SaveSTL uses). cubeResolution is the marching cube edge
// length; pass 0 to derive it from the bounding-box diagonal.
func MeshObject(s3 simplesdf.SDF3, cubeResolution float32) ([]ms3.Triangle, error) {
	if s3.Shader() == nil {
		return nil, errors.New("nil SDF3 shader")
	}
	sdf, err := gleval.NewCPUSDF3(s3.Shader())
	if err != nil {
		return nil, fmt.Errorf("NewCPUSDF3: %w", err)
	}
	if cubeResolution <= 0 {
		bb := sdf.Bounds()
		size := bb.Size()
		diag := math32.Sqrt(size.X*size.X + size.Y*size.Y + size.Z*size.Z)
		if diag <= 0 {
			diag = 1
		}
		cubeResolution = diag / defaultMeshDivisions
	}
	oct, err := glrender.NewOctreeRenderer(sdf, cubeResolution, 1<<14)
	if err != nil {
		return nil, fmt.Errorf("octree renderer: %w", err)
	}
	tris, err := glrender.RenderAll(oct, nil)
	if err != nil {
		return nil, fmt.Errorf("march: %w", err)
	}
	return tris, nil
}

// trianglesBounds returns the axis-aligned bounding box of a mesh.
func trianglesBounds(tris []ms3.Triangle) ms3.Box {
	if len(tris) == 0 {
		return ms3.Box{}
	}
	mn := tris[0][0]
	mx := tris[0][0]
	for _, t := range tris {
		for _, v := range t {
			mn = ms3.MinElem(mn, v)
			mx = ms3.MaxElem(mx, v)
		}
	}
	return ms3.Box{Min: mn, Max: mx}
}

// rasterizeMesh renders the triangle mesh from the orbit camera with a
// z-buffer and two-sided Lambertian shading, matching RenderAngled's look
// (teal tint, dark background). downscale < 1 means 1.
func rasterizeMesh(tris []ms3.Triangle, cp CameraParams, maxW, maxH, maxEdge, downscale int) (*image.NRGBA, image.Rectangle, error) {
	w, h := computeTargetSizeAngled(maxW, maxH, maxEdge)
	if downscale > 1 {
		w = max(8, w/downscale)
		h = max(8, h/downscale)
	}
	rect := image.Rect(0, 0, w, h)
	img := image.NewNRGBA(rect)
	for i := 0; i < w*h; i++ {
		off := i * 4
		img.Pix[off+0] = 25
		img.Pix[off+1] = 28
		img.Pix[off+2] = 35
		img.Pix[off+3] = 255
	}
	if len(tris) == 0 {
		return img, rect, nil
	}

	cam := cameraFor(trianglesBounds(tris), cp)
	aspect := float32(w) / float32(h)
	tanFOV := math32.Tan(cam.fov / 2)
	const near = 1e-4

	zbuf := make([]float32, w*h)
	for i := range zbuf {
		zbuf[i] = math32.Inf(1)
	}

	// project maps a world point to screen (x,y) + view depth; ok=false
	// when the point is at/behind the camera plane.
	project := func(p ms3.Vec) (sx, sy, vz float32, ok bool) {
		rel := ms3.Sub(p, cam.eye)
		vx := ms3.Dot(rel, cam.right)
		vy := ms3.Dot(rel, cam.up)
		vz = ms3.Dot(rel, cam.forward)
		if vz <= near {
			return 0, 0, vz, false
		}
		ndcX := (vx / vz) / (tanFOV * aspect)
		ndcY := (vy / vz) / tanFOV
		sx = (ndcX + 1) * 0.5 * float32(w)
		sy = (1 - ndcY) * 0.5 * float32(h)
		return sx, sy, vz, true
	}

	for _, tri := range tris {
		x0, y0, z0, ok0 := project(tri[0])
		x1, y1, z1, ok1 := project(tri[1])
		x2, y2, z2, ok2 := project(tri[2])
		if !ok0 || !ok1 || !ok2 {
			continue // any vertex behind the camera: skip (no near-clip)
		}

		// Two-sided flat shading from the world-space face normal.
		n := ms3.Cross(ms3.Sub(tri[1], tri[0]), ms3.Sub(tri[2], tri[0]))
		nl := ms3.Norm(n)
		shade := float32(0.6)
		if nl > 0 {
			d := ms3.Dot(ms3.Scale(1/nl, n), lightDir)
			if d < 0 {
				d = -d
			}
			shade = 0.25 + d*0.75
		}
		if shade > 1 {
			shade = 1
		}
		c := uint8(shade * 255)
		cr, cg, cb := uint8(float32(c)*0.85), uint8(float32(c)*0.95), c

		minX := int(math32.Floor(min3(x0, x1, x2)))
		maxX := int(math32.Ceil(max3(x0, x1, x2)))
		minY := int(math32.Floor(min3(y0, y1, y2)))
		maxY := int(math32.Ceil(max3(y0, y1, y2)))
		if minX < 0 {
			minX = 0
		}
		if minY < 0 {
			minY = 0
		}
		if maxX >= w {
			maxX = w - 1
		}
		if maxY >= h {
			maxY = h - 1
		}

		area := edge(x0, y0, x1, y1, x2, y2)
		if area == 0 {
			continue
		}
		invArea := 1 / area
		for py := minY; py <= maxY; py++ {
			fy := float32(py) + 0.5
			for px := minX; px <= maxX; px++ {
				fx := float32(px) + 0.5
				w0 := edge(x1, y1, x2, y2, fx, fy) * invArea
				w1 := edge(x2, y2, x0, y0, fx, fy) * invArea
				w2 := edge(x0, y0, x1, y1, fx, fy) * invArea
				// Inside test tolerant to winding direction.
				if (w0 < 0 || w1 < 0 || w2 < 0) && (w0 > 0 || w1 > 0 || w2 > 0) {
					continue
				}
				z := w0*z0 + w1*z1 + w2*z2
				idx := py*w + px
				if z >= zbuf[idx] {
					continue
				}
				zbuf[idx] = z
				off := idx * 4
				img.Pix[off+0] = cr
				img.Pix[off+1] = cg
				img.Pix[off+2] = cb
				img.Pix[off+3] = 255
			}
		}
	}
	return img, rect, nil
}

// edge is the signed area of the triangle (ax,ay),(bx,by),(px,py).
func edge(ax, ay, bx, by, px, py float32) float32 {
	return (px-ax)*(by-ay) - (py-ay)*(bx-ax)
}

func min3(a, b, c float32) float32 { return math32.Min(a, math32.Min(b, c)) }
func max3(a, b, c float32) float32 { return math32.Max(a, math32.Max(b, c)) }
