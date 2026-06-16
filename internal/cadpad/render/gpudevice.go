package render

import (
	"testing"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
	_ "github.com/gogpu/wgpu/hal/allbackends" // register Metal/Vulkan/etc. backends
)

// gpuDev / gpuErr hold the single gogpu device (or the failure to create one).
// They are created ONCE, on the executor goroutine, by gpuExecStart (see
// gpuexec.go) and read-only thereafter, so no further synchronization is needed
// for reads that happen on the executor thread or after gpuExecReady is closed.
var (
	gpuDev *wgpu.Device
	gpuErr error
)

// device returns the single gogpu device (or the init error). It is valid to
// call ONLY from within a gpuDo closure on the executor goroutine, where the
// device has already been created by gpuExecStart. It performs no creation of
// its own — that lives on the executor thread (createDevice in gpuexec.go) so
// every gogpu call, including device creation, runs on one stable OS thread.
func device() (*wgpu.Device, error) {
	return gpuDev, gpuErr
}

// isSoftwareAdapter reports whether the adapter is gogpu's CPU/software backend.
// gogpu exposes AdapterInfo.DeviceType and AdapterInfo.Backend; a software
// adapter reports DeviceTypeCPU and/or the empty (noop/software) backend.
func isSoftwareAdapter(a *wgpu.Adapter) bool {
	info := a.Info()
	return info.DeviceType == gputypes.DeviceTypeCPU || info.Backend == gputypes.BackendEmpty
}

// gpuAvailable reports whether a hardware GPU device was created. It triggers
// (and waits for) device init on the executor goroutine via gpuDo, then checks
// the resulting device/error. When there is no GPU, createDevice returns an
// error and this returns false promptly — it never blocks forever.
func gpuAvailable() bool {
	var ok bool
	// The closure runs on the executor thread, after device init has completed
	// (gpuDo waits on gpuExecReady first). It only reads the device, no GPU API
	// calls, so it cannot itself fail.
	_ = gpuDo(func() error {
		ok = gpuDev != nil && gpuErr == nil
		return nil
	})
	return ok
}

func requireGPU(t *testing.T) {
	t.Helper()
	if !gpuAvailable() {
		t.Skip("no GPU adapter; skipping GPU-execution test")
	}
}
