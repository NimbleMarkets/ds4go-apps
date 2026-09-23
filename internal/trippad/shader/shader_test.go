package shader

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/ntgpu"
	"github.com/gogpu/naga"
)

func TestUniformLayout(t *testing.T) {
	u := tripUniform{}
	if unsafe.Sizeof(u) != 96 || unsafe.Offsetof(u.Params) != 32 || unsafe.Offsetof(u.Frame) != 16 {
		t.Fatalf("uniform layout size=%d params=%d frame=%d", unsafe.Sizeof(u), unsafe.Offsetof(u.Params), unsafe.Offsetof(u.Frame))
	}
}

func TestDocumentBodyMapping(t *testing.T) {
	for _, src := range append(Starters(), Source{Name: "unicode", ShadeBody: "// 🌈\nreturn vec3<f32>(1.0);\n"}) {
		doc, err := BuildDocument(src)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(doc.Code, "\n")
		body := strings.Join(lines[doc.BodyStart-1:doc.BodyEnd], "\n")
		if body != src.ShadeBody {
			t.Fatalf("incorrect mapping for %s: %q", src.Name, body)
		}
		code, err := BuildWGSL(src)
		if err != nil || code != doc.Code {
			t.Fatal("compiler and language server received different modules")
		}
	}
}
func TestStartersCompileWithoutGPU(t *testing.T) {
	for _, src := range Starters() {
		t.Run(src.Name, func(t *testing.T) {
			code, err := BuildWGSL(src)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(code, "%SHADE%") || strings.Contains(code, "%ACCESSORS%") {
				t.Fatal("unexpanded template")
			}
			ast, err := naga.Parse(code)
			if err != nil {
				t.Fatal(err)
			}
			mod, err := naga.Lower(ast)
			if err != nil {
				t.Fatal(err)
			}
			errs, err := naga.Validate(mod)
			if err != nil || len(errs) > 0 {
				t.Fatalf("validation: %v %v", err, errs)
			}
		})
	}
}
func requireGPU(t *testing.T) *ntgpu.Executor {
	t.Helper()
	exec := ntgpu.DefaultExecutor()
	if !exec.Available() {
		t.Skip("hardware GPU unavailable")
	}
	return exec
}
func TestGPUStarters(t *testing.T) {
	p := NewPipeline(requireGPU(t))
	defer p.Close()
	for _, src := range Starters() {
		t.Run(src.Name, func(t *testing.T) {
			values, _ := params.New(src.Params)
			img, err := p.Render(src, values.Values(), 1, .033, 30, 64, 48)
			if err != nil {
				t.Fatal(err)
			}
			lit, varied := false, false
			for i := 0; i < len(img.Pix); i += 4 {
				if img.Pix[i+3] != 255 {
					t.Fatal("not opaque")
				}
				if img.Pix[i] != 0 || img.Pix[i+1] != 0 || img.Pix[i+2] != 0 {
					lit = true
				}
				if !bytes.Equal(img.Pix[:3], img.Pix[i:i+3]) {
					varied = true
				}
			}
			if !lit || !varied {
				t.Fatal("expected lit non-uniform frame")
			}
			later, err := p.Render(src, values.Values(), 3, .033, 90, 64, 48)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(img.Pix, later.Pix) {
				t.Fatal("time did not animate shader")
			}
		})
	}
	count := len(p.cache)
	bad := Starters()[0]
	bad.ShadeBody = "return invalid syntax;"
	if err := p.Compile(bad); err == nil {
		t.Fatal("invalid WGSL compiled")
	}
	if len(p.cache) != count {
		t.Fatal("failed compilation changed cache")
	}
}
func TestGPUAllParameterSlots(t *testing.T) {
	p := NewPipeline(requireGPU(t))
	defer p.Close()
	src := Source{Name: "layout"}
	var values [16]float32
	for i := range values {
		src.Params = append(src.Params, params.Param{Name: fmt.Sprintf("v%d", i), Min: 0, Max: 1, Step: .01})
		values[i] = float32(i+1) / 17
	}
	for i := range values {
		src.ShadeBody = fmt.Sprintf("return vec3<f32>(p_v%d(), f32(uni.frame)/255.0, uni.dt);", i)
		img, err := p.Render(src, values, 0, .5, 42, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		want := byte(values[i] * 255)
		if img.Pix[0] != want || img.Pix[1] != 42 || img.Pix[2] != 127 {
			t.Fatalf("slot %d: %v want %d,42,127,255", i, img.Pix, want)
		}
	}
	if len(p.cache) > 8 {
		t.Fatal("cache exceeded bound")
	}
}

func TestGPUVerticalGradientAcrossRows(t *testing.T) {
	p := NewPipeline(requireGPU(t))
	defer p.Close()
	src := Source{Name: "vertical-gradient", ShadeBody: "return vec3<f32>((uv.y + 1.0) * 0.5);"}
	// Non-workgroup-aligned dimensions expose row-stride and dispatch seams.
	for _, size := range [][2]int{{17, 257}, {257, 255}} {
		img, err := p.Render(src, [16]float32{}, 0, 0, 0, size[0], size[1])
		if err != nil {
			t.Fatal(err)
		}
		for y := 0; y < size[1]; y++ {
			want := int((float64(y) + .5) / float64(size[1]) * 255)
			for x := 0; x < size[0]; x++ {
				c := img.NRGBAAt(x, y)
				if d := int(c.R) - want; d < -1 || d > 1 || c.R != c.G || c.R != c.B || c.A != 255 {
					t.Fatalf("%v pixel (%d,%d): %v, want gray %d", size, x, y, c, want)
				}
				if y > 0 && c.R < img.NRGBAAt(x, y-1).R {
					t.Fatalf("gradient reverses at row %d", y)
				}
			}
		}
	}
}

// Repeated readbacks used to spawn an unpinned PollWait goroutine per frame.
// Exercise mapping under scheduler/GC pressure, including concurrent preview
// and animation calls through the same pipeline.
func TestGPURepeatedReadback(t *testing.T) {
	p := NewPipeline(requireGPU(t))
	defer p.Close()
	src := Source{Name: "readback", ShadeBody: "return vec3<f32>(f32(uni.frame % 256u)/255.0, 0.0, 1.0);"}
	var wg sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		wg.Go(func() {
			for i := 0; i < 256; i++ {
				img, err := p.Render(src, [16]float32{}, 0, 0, uint32(i), 128, 96)
				if err != nil {
					t.Error(err)
					return
				}
				for offset := 0; offset < len(img.Pix); offset += 4 {
					if d := int(img.Pix[offset]) - i; d < -1 || d > 1 || img.Pix[offset+2] != 255 || img.Pix[offset+3] != 255 {
						t.Errorf("frame %d returned stale or corrupt pixels: %v", i, img.Pix[offset:offset+4])
						return
					}
				}
				if i%16 == 0 {
					runtime.GC()
				}
			}
		})
	}
	wg.Wait()
}
