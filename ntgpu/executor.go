package ntgpu

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
	_ "github.com/gogpu/wgpu/hal/allbackends" // register Metal/Vulkan/etc. backends
)

// Options controls device selection for an Executor.
type Options struct {
	PowerPreference      gputypes.PowerPreference
	AllowSoftware        bool
	ForceFallbackAdapter bool
}

// DefaultOptions requests a high-performance hardware adapter.
var DefaultOptions = Options{
	PowerPreference: wgpu.PowerPreferenceHighPerformance,
}

type job struct {
	fn   func(*wgpu.Device) error
	done chan error
}

// Executor serializes all wgpu calls onto one locked OS thread. This is useful
// for Metal-backed gogpu workloads where device, pipeline, dispatch, and Release
// calls should all happen on a stable thread and in a single resource order.
type Executor struct {
	opts Options

	once  sync.Once
	jobs  chan job
	ready chan struct{}

	dev *wgpu.Device
	err error
}

// NewExecutor constructs a lazy GPU executor. Device creation happens on the
// executor goroutine the first time Do, DoFunc, Device, or Available is called.
func NewExecutor(opts Options) *Executor {
	if opts.PowerPreference == 0 {
		opts.PowerPreference = DefaultOptions.PowerPreference
	}
	return &Executor{opts: opts}
}

var defaultExecutor = NewExecutor(DefaultOptions)

// DefaultExecutor returns the process-wide default executor.
func DefaultExecutor() *Executor { return defaultExecutor }

func (e *Executor) start() {
	e.once.Do(func() {
		e.jobs = make(chan job)
		e.ready = make(chan struct{})
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()

			e.dev, e.err = createDevice(e.opts)
			close(e.ready)

			for j := range e.jobs {
				j.done <- j.fn(e.dev)
			}
		}()
	})
}

// Do runs fn on the executor's GPU thread after a hardware device has been
// created. If device creation failed, Do returns that error without running fn.
//
// Do blocks until fn returns. A function already running inside Do must not call
// Do or DoFunc on the same Executor again; doing so queues work behind itself
// and deadlocks.
func (e *Executor) Do(fn func(*wgpu.Device) error) error {
	e.start()
	<-e.ready
	if e.err != nil {
		return e.err
	}
	j := job{fn: fn, done: make(chan error, 1)}
	e.jobs <- j
	return <-j.done
}

// DoFunc is a convenience wrapper for work that does not need the device value
// directly, such as releasing already-captured resources.
func (e *Executor) DoFunc(fn func() error) error {
	return e.Do(func(*wgpu.Device) error { return fn() })
}

// Available reports whether the executor created a hardware GPU device.
func (e *Executor) Available() bool {
	e.start()
	<-e.ready
	return e.dev != nil && e.err == nil
}

func createDevice(opts Options) (*wgpu.Device, error) {
	inst, err := wgpu.CreateInstance(nil)
	if err != nil {
		return nil, fmt.Errorf("create instance: %w", err)
	}
	adapter, err := inst.RequestAdapter(&wgpu.RequestAdapterOptions{
		PowerPreference:      opts.PowerPreference,
		ForceFallbackAdapter: opts.ForceFallbackAdapter,
	})
	if err != nil {
		return nil, fmt.Errorf("request adapter: %w", err)
	}
	if adapter == nil {
		return nil, errors.New("no GPU adapter")
	}
	if !opts.AllowSoftware && IsSoftwareAdapter(adapter) {
		return nil, errors.New("only software adapter available")
	}
	dev, err := adapter.RequestDevice(nil)
	if err != nil {
		return nil, fmt.Errorf("request device: %w", err)
	}
	return dev, nil
}

// IsSoftwareAdapter reports whether an adapter is gogpu's CPU/software backend.
func IsSoftwareAdapter(a *wgpu.Adapter) bool {
	info := a.Info()
	return info.DeviceType == gputypes.DeviceTypeCPU || info.Backend == gputypes.BackendEmpty
}
