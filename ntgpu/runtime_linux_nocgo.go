//go:build linux && !cgo

package ntgpu

// Linux builds use -tags=nofakecgo to disable goffi's duplicate FFI runtime.
// Import purego here so standalone GPU packages and their tests provide that
// runtime too, even when they do not otherwise import ds4go.
import _ "github.com/ebitengine/purego"
