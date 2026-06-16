package render

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/gogpu/wgpu"
)

// Single-goroutine GPU executor.
//
// gogpu/Metal prefers all GPU API calls on one stable OS thread, and the live
// viewport touches gogpu from two goroutines concurrently: a bubbletea tea.Cmd
// goroutine rendering, and the bubbletea update goroutine invalidating the
// pipeline cache (which Release()s GPU objects). Running both directly opens a
// use-after-Release window (a Release racing an in-flight dispatch) and risks
// Metal misbehaving off its preferred thread.
//
// The fix: ALL gogpu work — device creation, pipeline build, dispatch, and
// Release — runs as a closure submitted to gpuDo, which funnels it onto a
// single LockOSThread'd goroutine. Submissions are serialized through one
// channel, so a Release can never interleave with a dispatch; it simply queues
// after any in-flight GPU work.
//
// RE-ENTRANCY: gpuDo BLOCKS the caller until the closure finishes on the
// executor goroutine. A closure already running ON the executor must therefore
// NEVER call gpuDo again — it would enqueue work that the (busy) executor
// cannot pick up until the current closure returns, deadlocking on itself.
// Inside an executor closure, call the raw GPU helpers (buildGPUPipeline,
// dispatchPipeline, runComputeDouble, etc.) DIRECTLY, never via gpuDo.
//
// MUTEX ORDERING vs Renderer.mu: never hold Renderer.mu while blocking on
// gpuDo. Callers split map bookkeeping (under mu) from GPU calls (inside
// gpuDo): read/replace the cache entry under mu, release mu, then gpuDo the
// GPU work. This avoids a lock-order inversion if a future executor closure
// ever needed mu.

// gpuJob is one unit of GPU work plus a one-shot reply channel carrying its
// error back to the submitting goroutine.
type gpuJob struct {
	fn   func() error
	done chan error
}

var (
	gpuExecOnce  sync.Once
	gpuJobCh     chan gpuJob
	gpuExecReady chan struct{} // closed once the executor has attempted device init
)

// gpuExecStart lazily launches the executor goroutine exactly once. The
// goroutine pins itself to an OS thread, creates and owns the *wgpu.Device on
// that thread, signals readiness, then serves jobs forever.
func gpuExecStart() {
	gpuExecOnce.Do(func() {
		gpuJobCh = make(chan gpuJob)
		gpuExecReady = make(chan struct{})
		go func() {
			// Pin to one OS thread for the executor's whole life so every
			// gogpu/Metal call lands on the same thread. The goroutine never
			// returns, so the UnlockOSThread defer is only for tidiness.
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()

			// Device creation MUST happen on this thread (the one all later
			// GPU calls use). Populate the package-level gpuDev/gpuErr here.
			gpuDev, gpuErr = createDevice()
			close(gpuExecReady)

			for job := range gpuJobCh {
				job.done <- job.fn()
			}
		}()
	})
}

// gpuDo submits fn to the executor goroutine and blocks until it has run there,
// returning fn's error. It guarantees fn executes on the single GPU thread,
// serialized after any previously-submitted GPU work.
//
// Do NOT call gpuDo from within a closure already running on the executor (see
// the re-entrancy note above) — that deadlocks.
func gpuDo(fn func() error) error {
	gpuExecStart()
	<-gpuExecReady // ensure device init has been attempted before any job runs
	job := gpuJob{fn: fn, done: make(chan error, 1)}
	gpuJobCh <- job
	return <-job.done
}

// createDevice performs the actual gogpu instance/adapter/device creation. It
// runs ONLY on the executor goroutine (called from gpuExecStart). It returns an
// error (never a software-backend device) when no hardware GPU is present.
func createDevice() (*wgpu.Device, error) {
	inst, err := wgpu.CreateInstance(nil)
	if err != nil {
		return nil, fmt.Errorf("create instance: %w", err)
	}
	adapter, err := inst.RequestAdapter(&wgpu.RequestAdapterOptions{
		PowerPreference: wgpu.PowerPreferenceHighPerformance,
		// Never request a software fallback adapter.
		ForceFallbackAdapter: false,
	})
	if err != nil {
		return nil, fmt.Errorf("request adapter: %w", err)
	}
	if adapter == nil {
		return nil, errors.New("no GPU adapter")
	}
	// Reject the software backend: we want a real GPU, not CPU emulation.
	if isSoftwareAdapter(adapter) {
		return nil, errors.New("only software adapter available")
	}
	dev, err := adapter.RequestDevice(nil)
	if err != nil {
		return nil, fmt.Errorf("request device: %w", err)
	}
	return dev, nil
}
