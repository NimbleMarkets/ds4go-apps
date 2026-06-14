package render

import (
	"bytes"
	"testing"

	"github.com/hpinc/go3mf"
	"github.com/soypat/geometry/ms3"
)

func TestWriteMesh3MFRoundTrip(t *testing.T) {
	// A unit tetrahedron: 4 verts, 4 faces, but supplied as 12 loose
	// triangle corners to exercise vertex dedup.
	a := ms3.Vec{X: 0, Y: 0, Z: 0}
	b := ms3.Vec{X: 1, Y: 0, Z: 0}
	c := ms3.Vec{X: 0, Y: 1, Z: 0}
	d := ms3.Vec{X: 0, Y: 0, Z: 1}
	tris := []ms3.Triangle{{a, b, c}, {a, b, d}, {a, c, d}, {b, c, d}}

	var buf bytes.Buffer
	if err := WriteMesh3MF(&buf, "tetra", tris); err != nil {
		t.Fatalf("WriteMesh3MF: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty 3mf output")
	}

	var got go3mf.Model
	dec := go3mf.NewDecoder(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode 3mf: %v", err)
	}
	if got.Units != go3mf.UnitMillimeter {
		t.Errorf("units = %v, want millimeter", got.Units)
	}
	if len(got.Resources.Objects) != 1 {
		t.Fatalf("objects = %d, want 1", len(got.Resources.Objects))
	}
	obj := got.Resources.Objects[0]
	if obj.Name != "tetra" {
		t.Errorf("object name = %q, want tetra", obj.Name)
	}
	if obj.Mesh == nil {
		t.Fatal("object has no mesh")
	}
	if n := len(obj.Mesh.Vertices.Vertex); n != 4 {
		t.Errorf("vertices = %d, want 4 (deduped)", n)
	}
	if n := len(obj.Mesh.Triangles.Triangle); n != 4 {
		t.Errorf("triangles = %d, want 4", n)
	}
	if len(got.Build.Items) != 1 {
		t.Errorf("build items = %d, want 1", len(got.Build.Items))
	}
}

func TestWriteMesh3MFEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMesh3MF(&buf, "empty", nil); err == nil {
		t.Error("expected error for empty mesh")
	}
}
