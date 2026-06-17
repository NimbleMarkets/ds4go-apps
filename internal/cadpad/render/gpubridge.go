package render

import (
	"errors"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/ntgpu"
	"github.com/gogpu/wgpu"
)

var gpuExecutor = ntgpu.DefaultExecutor()
var activeGPUDevice *wgpu.Device

// gpuDo submits fn to the shared ntgpu executor and blocks until it runs on the
// single locked GPU thread. Cadpad keeps this wrapper so all render helpers can
// document the local "must be inside gpuDo" contract without owning executor
// internals.
func gpuDo(fn func() error) error {
	return gpuExecutor.Do(func(dev *wgpu.Device) error {
		activeGPUDevice = dev
		defer func() { activeGPUDevice = nil }()
		return fn()
	})
}

// device returns the executor-owned device. It is only valid to use from inside
// a gpuDo closure, where wgpu calls stay on the executor thread.
func device() (*wgpu.Device, error) {
	if activeGPUDevice == nil {
		return nil, errors.New("gpu device requested outside gpuDo")
	}
	return activeGPUDevice, nil
}

func isSoftwareAdapter(a *wgpu.Adapter) bool {
	return ntgpu.IsSoftwareAdapter(a)
}

func gpuAvailable() bool {
	return gpuExecutor.Available()
}

func requireGPU(t *testing.T) {
	t.Helper()
	if !gpuAvailable() {
		t.Skip("no GPU adapter; skipping GPU-execution test")
	}
}
