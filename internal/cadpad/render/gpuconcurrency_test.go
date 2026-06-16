package render

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/soypat/gsdf/gsdfaux/simplesdf"
)

// TestGPUConcurrentRenderAndInvalidate is the gate proving the serialized GPU
// executor closes the use-after-Release window. Several goroutines hammer
// RenderAngledAuto (GPU build+dispatch) on a couple of shapes while another
// goroutine concurrently Invalidate()s / ClearCache()s, which Release()s cached
// GPU pipelines. Before the executor, an Invalidate could free a pipeline
// mid-dispatch; now every build/dispatch/Release is serialized onto one GPU
// thread, so a render either reuses a live pipeline or transparently rebuilds —
// never touches freed memory.
//
// Run under `go test -race` to assert there is no data race, and the test must
// finish without panic/crash. Every render must return a valid image at the
// requested dims (or a clean error), never a corrupt/use-after-free result.
//
// NOTE on -race: gogpu's Metal completion-block callback
// (hal/metal/objc.go getGPUCompletionBlockInvoke) does pointer arithmetic that
// Go's checkptr instrumentation — which -race enables — flags as
// "pointer arithmetic result points to invalid allocation", a fatal error that
// fires on ANY single GPU dispatch (e.g. TestSpikeSphereRaymarch -race), not
// just concurrent ones. It is a pre-existing gogpu issue, independent of this
// executor. To exercise the actual race DETECTOR (which finds no data race
// here), run with checkptr disabled:
//
//	go test -run GPUConcurrent -race -gcflags=all=-d=checkptr=0 ./internal/cadpad/render/
func TestGPUConcurrentRenderAndInvalidate(t *testing.T) {
	requireGPU(t)

	r, err := NewRenderer(PreviewConfig{})
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	// Two shapes under two stable names: renderers reuse/rebuild their cached
	// pipelines while the invalidator keeps yanking them out from under.
	shapes := map[string]simplesdf.SDF3{
		"conc_sphere": simplesdf.Sphere(3),
		"conc_box":    simplesdf.Box(4, 3, 2, 0),
	}
	names := []string{"conc_sphere", "conc_box"}

	cp := CameraParams{Azimuth: 0.6, Elevation: 0.4, Zoom: 1}
	const maxW, maxH, downscale = 64, 64, 1
	const renderers = 8
	const iters = 60

	var stop atomic.Bool
	var renderFail atomic.Int64

	// renderWG tracks only the renderer goroutines so we can stop the
	// invalidator once they've all finished their fixed iteration counts.
	var renderWG sync.WaitGroup
	for g := 0; g < renderers; g++ {
		renderWG.Add(1)
		go func(g int) {
			defer renderWG.Done()
			for i := 0; i < iters; i++ {
				name := names[(g+i)%len(names)]
				s3 := shapes[name]
				// Vary the camera a touch so cache hits and dispatches interleave.
				cp2 := cp
				cp2.Azimuth += float32(i) * 0.01
				img, rect, mode, err := r.RenderAngledAuto(s3, name, cp2, maxW, maxH, downscale)
				if err != nil {
					// A clean error is acceptable (never a crash/use-after-free).
					// RenderAngledAuto falls back to CPU on GPU failure, so we
					// don't expect one; record and continue.
					renderFail.Add(1)
					continue
				}
				if img == nil {
					t.Errorf("goroutine %d iter %d: nil image (mode %s)", g, i, mode)
					return
				}
				b := img.Bounds()
				if b.Dx() != rect.Dx() || b.Dy() != rect.Dy() {
					t.Errorf("goroutine %d iter %d: image bounds %v != rect %v", g, i, b, rect)
					return
				}
				if b.Dx() <= 0 || b.Dy() <= 0 {
					t.Errorf("goroutine %d iter %d: degenerate image bounds %v", g, i, b)
					return
				}
			}
		}(g)
	}

	// Invalidator goroutine: continuously frees cached pipelines while renders
	// are in flight. This is what would trip a use-after-Release without the
	// executor serialization. It runs until the renderers are done.
	var invWG sync.WaitGroup
	invWG.Add(1)
	go func() {
		defer invWG.Done()
		for !stop.Load() {
			for _, name := range names {
				r.Invalidate(name)
			}
			r.ClearCache()
		}
	}()

	renderWG.Wait()
	stop.Store(true)
	invWG.Wait()

	if t.Failed() {
		return
	}
	t.Logf("concurrent renders OK; renderFail(clean errors)=%d", renderFail.Load())
}
