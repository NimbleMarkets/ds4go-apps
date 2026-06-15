package render

import (
	"math"
	"testing"
)

func TestGPUComputeRoundTrip(t *testing.T) {
	requireGPU(t)
	in := make([]float32, 256)
	for i := range in {
		in[i] = float32(i)
	}
	out, err := runComputeDouble(in)
	if err != nil {
		t.Fatalf("runComputeDouble: %v", err)
	}
	for i := range in {
		if math.Abs(float64(out[i]-in[i]*2)) > 1e-5 {
			t.Fatalf("out[%d]=%v want %v", i, out[i], in[i]*2)
		}
	}
}
