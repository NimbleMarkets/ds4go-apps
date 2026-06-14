// export3mf.go: write a triangle mesh as a 3MF (3D Manufacturing Format)
// file. 3MF is the modern STL replacement — a zipped XML package that
// carries units, object names, and metadata, and is accepted by every
// current slicer. The mesh is the same octree triangulation STL export and
// the 3D preview use.

package render

import (
	"errors"
	"io"

	"github.com/chewxy/math32"
	"github.com/hpinc/go3mf"
	"github.com/soypat/geometry/ms3"
	"github.com/soypat/gsdf/gleval"
	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// WriteSDF3MF marches an SDF to a triangle mesh at the given resolution and
// writes it as 3MF. divisions sets the marching cube count along the
// bounding-box diagonal (higher = finer); <= 0 uses the preview default.
func WriteSDF3MF(w io.Writer, name string, s3 simplesdf.SDF3, divisions int) error {
	if s3.Shader() == nil {
		return errors.New("3mf export: nil SDF3")
	}
	cubeRes := float32(0)
	if divisions > 0 {
		sdf, err := gleval.NewCPUSDF3(s3.Shader())
		if err != nil {
			return err
		}
		sz := sdf.Bounds().Size()
		diag := math32.Sqrt(sz.X*sz.X + sz.Y*sz.Y + sz.Z*sz.Z)
		if diag <= 0 {
			diag = 1
		}
		cubeRes = diag / float32(divisions)
	}
	tris, err := MeshObject(s3, cubeRes)
	if err != nil {
		return err
	}
	return WriteMesh3MF(w, name, tris)
}

// WriteMesh3MF encodes the triangle mesh as a single named 3MF object (in
// millimeters) to w. Shared vertices are deduplicated so the file indexes
// triangles by vertex like a proper mesh rather than repeating corners.
func WriteMesh3MF(w io.Writer, name string, tris []ms3.Triangle) error {
	if len(tris) == 0 {
		return errors.New("3mf export: empty mesh")
	}

	verts := make([]go3mf.Point3D, 0, len(tris))
	index := make(map[[3]float32]uint32, len(tris))
	intern := func(v ms3.Vec) uint32 {
		k := [3]float32{v.X, v.Y, v.Z}
		if id, ok := index[k]; ok {
			return id
		}
		id := uint32(len(verts))
		verts = append(verts, go3mf.Point3D{v.X, v.Y, v.Z})
		index[k] = id
		return id
	}

	faces := make([]go3mf.Triangle, 0, len(tris))
	for _, t := range tris {
		v1, v2, v3 := intern(t[0]), intern(t[1]), intern(t[2])
		if v1 == v2 || v2 == v3 || v1 == v3 {
			continue // degenerate after dedup
		}
		faces = append(faces, go3mf.Triangle{V1: v1, V2: v2, V3: v3})
	}
	if len(faces) == 0 {
		return errors.New("3mf export: mesh has no non-degenerate triangles")
	}

	model := &go3mf.Model{Units: go3mf.UnitMillimeter}
	obj := &go3mf.Object{
		ID:   1,
		Name: name,
		Type: go3mf.ObjectTypeModel,
		Mesh: &go3mf.Mesh{
			Vertices:  go3mf.Vertices{Vertex: verts},
			Triangles: go3mf.Triangles{Triangle: faces},
		},
	}
	model.Resources.Objects = append(model.Resources.Objects, obj)
	model.Build.Items = append(model.Build.Items, &go3mf.Item{ObjectID: obj.ID})

	return go3mf.NewEncoder(w).Encode(model)
}
