package ntgpu

import (
	"strings"
	"testing"
)

func TestDispatchRGBA8ValidatesRequiredInputs(t *testing.T) {
	_, err := DispatchRGBA8(nil, ImageDispatch{})
	if err == nil || !strings.Contains(err.Error(), "nil device") {
		t.Fatalf("DispatchRGBA8 nil device error = %v, want nil device", err)
	}
}
