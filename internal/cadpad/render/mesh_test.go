package render

import (
	"testing"

	"github.com/soypat/geometry/ms3"
	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

func TestMeshObjectProducesTriangles(t *testing.T) {
	s := simplesdf.Sphere(5)
	tris, err := MeshObject(s, 0)
	if err != nil {
		t.Fatalf("MeshObject: %v", err)
	}
	if len(tris) < 100 {
		t.Fatalf("sphere meshed to %d triangles, want a closed surface", len(tris))
	}
	// Every vertex must lie near the sphere surface (radius 5).
	for _, tri := range tris {
		for _, v := range tri {
			r := v.X*v.X + v.Y*v.Y + v.Z*v.Z
			if r < 9 || r > 49 { // 3..7 radius tolerance for a coarse mesh
				t.Fatalf("vertex %+v off the sphere surface (r^2=%.1f)", v, r)
			}
		}
	}
}

func TestRenderMeshShadesForeground(t *testing.T) {
	s := simplesdf.Sphere(5)
	tris, err := MeshObject(s, 0)
	if err != nil {
		t.Fatal(err)
	}
	cp := CameraParams{Zoom: 1}

	img, rect, err := rasterizeMesh(tris, cp, 120, 120, 768, 1)
	if err != nil {
		t.Fatalf("rasterizeMesh: %v", err)
	}
	if rect.Dx() == 0 || rect.Dy() == 0 {
		t.Fatal("empty raster rect")
	}
	// Center pixel should be foreground (lit surface), not the bg color.
	cx, cy := rect.Dx()/2, rect.Dy()/2
	off := (cy*rect.Dx() + cx) * 4
	rB, gB, bB := img.Pix[off], img.Pix[off+1], img.Pix[off+2]
	if rB == 25 && gB == 28 && bB == 35 {
		t.Error("center pixel is background — mesh not rasterized into view")
	}

	// A clearly-outside corner pixel should stay background.
	if c := img.Pix[0]; c != 25 {
		t.Errorf("top-left corner R=%d, want background 25", c)
	}
}

func TestRasterizeMeshDownscaleShrinks(t *testing.T) {
	s := simplesdf.Box(4, 4, 4, 0)
	tris, _ := MeshObject(s, 0)
	cp := CameraParams{Zoom: 1}

	full, _, _ := rasterizeMesh(tris, cp, 240, 240, 768, 1)
	low, _, _ := rasterizeMesh(tris, cp, 240, 240, 768, 3)
	fb, lb := full.Bounds(), low.Bounds()
	if lb.Dx()*2 >= fb.Dx() {
		t.Errorf("downscale=3 raster %v not smaller than full %v", lb, fb)
	}
}

func TestRenderAngledMeshCachesMesh(t *testing.T) {
	r, err := NewRenderer(PreviewConfig{MaxEdge: 96, EvalBuffer: 4096})
	if err != nil {
		t.Fatal(err)
	}
	s := simplesdf.Sphere(4)
	cp := CameraParams{Zoom: 1}

	if _, _, err := r.RenderAngledMesh(s, "ball", cp, 120, 120, 1); err != nil {
		t.Fatalf("first RenderAngledMesh: %v", err)
	}
	if _, ok := r.meshCache["ball"]; !ok {
		t.Fatal("mesh not cached after first render")
	}

	// Poison the cache with a sentinel mesh; a cache hit must reuse it
	// verbatim (no re-mesh would overwrite the sentinel).
	sentinel := []ms3.Triangle{{}}
	r.meshCache["ball"] = sentinel
	img, _, err := r.RenderAngledMesh(s, "ball", cp, 120, 120, 1)
	if err != nil {
		t.Fatalf("second RenderAngledMesh: %v", err)
	}
	if img == nil {
		t.Fatal("nil image on cache hit")
	}
	if len(r.meshCache["ball"]) != 1 {
		t.Errorf("cache hit re-meshed (%d tris), want the sentinel preserved", len(r.meshCache["ball"]))
	}

	// Invalidate drops the mesh; next render re-meshes.
	r.Invalidate("ball")
	if _, ok := r.meshCache["ball"]; ok {
		t.Error("Invalidate did not drop the cached mesh")
	}
	if _, _, err := r.RenderAngledMesh(s, "ball", cp, 120, 120, 1); err != nil {
		t.Fatal(err)
	}
	if got := r.meshCache["ball"]; len(got) == 0 {
		t.Error("re-mesh after invalidate produced no triangles")
	}

	r.ClearCache()
	if len(r.meshCache) != 0 {
		t.Error("ClearCache did not drop meshes")
	}
}
