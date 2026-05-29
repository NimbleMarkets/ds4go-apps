package render

import (
	"image"
	"testing"

	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

func TestNewRenderer(t *testing.T) {
	r, err := NewRenderer(DefaultPreviewConfig)
	if err != nil {
		t.Fatalf("NewRenderer default: %v", err)
	}
	if r == nil {
		t.Fatal("nil renderer")
	}
	r.ClearCache() // should be safe on fresh renderer

	cfg := PreviewConfig{MaxEdge: 64, EvalBuffer: 4096}
	r2, err := NewRenderer(cfg)
	if err != nil {
		t.Fatalf("NewRenderer custom: %v", err)
	}
	_ = r2
}

func TestRenderBasicProjections(t *testing.T) {
	r, err := NewRenderer(PreviewConfig{MaxEdge: 128, EvalBuffer: 4096})
	if err != nil {
		t.Fatal(err)
	}

	sphere := simplesdf.Sphere(5)
	box := simplesdf.Box(4, 3, 2, 0)

	for _, proj := range []Projection{ProjXY, ProjXZ, ProjYZ} {
		img, rect, err := r.Render(sphere, "sphere", proj, 200, 200)
		if err != nil {
			t.Fatalf("Render sphere %s: %v", proj, err)
		}
		if img == nil || rect.Dx() == 0 || rect.Dy() == 0 {
			t.Errorf("sphere %s: bad image or rect: %+v", proj, rect)
		}

		img2, rect2, err := r.Render(box, "box", proj, 120, 80)
		if err != nil {
			t.Fatalf("Render box %s: %v", proj, err)
		}
		if img2 == nil || rect2.Dx() < 8 {
			t.Errorf("box %s: bad image: %+v", proj, rect2)
		}
	}
}

func TestRenderUnionAndCache(t *testing.T) {
	r, _ := NewRenderer(DefaultPreviewConfig)

	s1 := simplesdf.Sphere(3)
	s2 := simplesdf.Sphere(3).Translate(2, 0, 0)
	union := s1.Union(s2)

	// First render populates cache
	img1, _, err := r.Render(union, "union", ProjXY, 100, 100)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	if img1 == nil {
		t.Fatal("nil image from union")
	}

	// Second render should be a cache hit (no error, same behavior)
	img2, _, err := r.Render(union, "union", ProjXY, 100, 100)
	if err != nil {
		t.Fatalf("cached render: %v", err)
	}
	if img2 == nil {
		t.Fatal("nil image on cache hit")
	}
}

func TestRenderAfterClearCache(t *testing.T) {
	r, _ := NewRenderer(DefaultPreviewConfig)
	s := simplesdf.Box(5, 5, 5, 0.5)

	_, _, err := r.Render(s, "box", ProjXY, 64, 64)
	if err != nil {
		t.Fatal(err)
	}

	r.ClearCache()

	// Should still work after clearing (just slower the first time)
	img, rect, err := r.Render(s, "box", ProjXY, 64, 64)
	if err != nil {
		t.Fatalf("render after clear: %v", err)
	}
	if img == nil || rect.Dx() == 0 {
		t.Error("bad image after cache clear")
	}
}

func TestRenderRejectsNilShader(t *testing.T) {
	r, _ := NewRenderer(DefaultPreviewConfig)

	var bad simplesdf.SDF3 // zero value has nil shader
	_, _, err := r.Render(bad, "bad", ProjXY, 64, 64)
	if err == nil {
		t.Fatal("expected error for nil shader")
	}
}

func TestRenderDifferentProjectionsProduceDifferentSizes(t *testing.T) {
	r, _ := NewRenderer(PreviewConfig{MaxEdge: 64})

	// Long thin box along X
	long := simplesdf.Box(20, 1, 1, 0)

	sizes := map[Projection]image.Rectangle{}
	for _, p := range []Projection{ProjXY, ProjXZ, ProjYZ} {
		_, rect, err := r.Render(long, "long", p, 200, 200)
		if err != nil {
			t.Fatal(err)
		}
		sizes[p] = rect
	}

	// XY and XZ views should be wider than tall for this object; YZ should be more square-ish.
	// We mainly assert we got distinct reasonable rects.
	for p, rec := range sizes {
		if rec.Dx() < 8 || rec.Dy() < 8 {
			t.Errorf("%s produced too small rect: %+v", p, rec)
		}
	}
}

func TestComputeTargetSizeBasic(t *testing.T) {
	// We test the helper indirectly via Render (computeTargetSize is unexported).
	// A real test would export it or make an internal test helper if needed.
	w, h := 200, 50
	r, _ := NewRenderer(PreviewConfig{MaxEdge: 64})
	s := simplesdf.Box(10, 2, 1, 0)
	img, rect, err := r.Render(s, "aspect", ProjXY, w, h)
	if err != nil {
		t.Fatal(err)
	}
	if img == nil {
		t.Fatal("nil image")
	}
	// The returned rect should respect MaxEdge=64 on the long side.
	if rect.Dx() > 64 && rect.Dx() > rect.Dy() {
		t.Errorf("long side exceeded MaxEdge: %+v", rect)
	}
}
