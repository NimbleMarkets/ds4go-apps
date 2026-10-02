// Package padtest holds test support for code that drives picture widgets.
package padtest

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// sharedProbeID mirrors the picture package's unexported t=s query image ID
// (its Kitty probe ID plus one); there is no exported way to set the medium.
const sharedProbeID = 42069102

// AllowKittySharedMemory resolves the process-wide t=s capability as if the
// terminal had answered the widget's shared-memory query with OK. The widget
// only sends shared-memory frames once that reply arrives, and ntcharts offers
// no force override, so tests of the shm transport replay the reply through the
// same Update path a real terminal uses. The capability is process-global and
// stays resolved for the rest of the test binary.
func AllowKittySharedMemory(t testing.TB) {
	t.Helper()
	m := picture.New()
	m.Update(uv.KittyGraphicsEvent{Options: kitty.Options{ID: sharedProbeID}, Payload: []byte("OK")})
}
