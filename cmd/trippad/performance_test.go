package main

import (
	"image"
	"math"
	"testing"

	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

func TestRasterMatchesKittyUploadDimensions(t *testing.T) {
	for _, cell := range [][2]int{{10, 20}, {9, 19}, {32, 64}} {
		m := testModel(t)
		m.width, m.height = 160, 58
		m.pic.SetCellPixelSize(cell[0], cell[1])
		cmd := m.renderCmd()
		if cmd == nil {
			t.Fatal("missing render")
		}
		frame := cmd().(frameMsg)
		cols, rows := m.viewport()
		factor := m.pic.KittyResolutionFactor()
		w, h := cols*max(1, int(float64(cell[0])*factor)), rows*max(1, int(float64(cell[1])*factor))
		if frame.img.Bounds().Dx() != w || frame.img.Bounds().Dy() != h {
			t.Fatalf("GPU raster %v differs from upload %dx%d", frame.img.Bounds(), w, h)
		}
		if w*rows*cell[1] != h*cols*cell[0] || math.IsNaN(factor) {
			t.Fatalf("source and terminal aspect ratios differ: %dx%d cell=%v factor=%f", w, h, cell, factor)
		}
	}
}

func TestResolutionHasUniformRowMapping(t *testing.T) {
	for cw := 1; cw <= 64; cw++ {
		for ch := 1; ch <= 96; ch++ {
			for downscale := 1; downscale <= 8; downscale++ {
				f, w, h := viewportResolution(128, 48, cw, ch, downscale)
				pw, ph := max(1, int(float64(cw)*f)), max(1, int(float64(ch)*f))
				if w != 128*pw || h != 48*ph || pw*ch != ph*cw || f <= 0 || f > 1 {
					t.Fatalf("cell=%dx%d downscale=%d factor=%.18f raster=%dx%d upload cell=%dx%d", cw, ch, downscale, f, w, h, pw, ph)
				}
				// No aspect padding: the source interval of every terminal row
				// is exactly [row*ph, (row+1)*ph], without overlaps or gaps.
				for row := 0; row < 48; row++ {
					start := math.Round(float64(row*ch) * float64(w) / float64(128*cw))
					end := math.Round(float64((row+1)*ch) * float64(w) / float64(128*cw))
					if start != float64(row*ph) || end-start != float64(ph) {
						t.Fatal("nonuniform source-row mapping")
					}
				}
			}
		}
	}
	// Ordinary even cells retain the fast half-resolution path.
	f, w, h := viewportResolution(128, 48, 10, 20, 2)
	if math.Abs(f-.5) > 1e-15 || w != 640 || h != 480 {
		t.Fatalf("lost fast path: %f %dx%d", f, w, h)
	}
}

func TestHiddenViewportSkipsGPUWorkAndResumes(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 160, 58
	for _, kind := range []string{"source", "thinking"} {
		m.inspect.kind = kind
		if m.renderCmd() != nil || m.rendering || !m.dirty {
			t.Fatal("hidden viewport consumed a render")
		}
	}
	m.inspect.kind = ""
	m.showLog = true
	if m.renderCmd() != nil {
		t.Fatal("log overlay didn't suspend rendering")
	}
	m.showLog = false
	if m.renderCmd() == nil {
		t.Fatal("rendering didn't resume after closing overlay")
	}
}

// Measures CPU preparation + encoding at a representative large viewport;
// terminal transport and GPU time are deliberately excluded.
func BenchmarkPictureEncode(b *testing.B) {
	previous := picture.KittySupported()
	picture.ForceKittyCapability(picture.KittyCapabilitySupported)
	defer picture.ForceKittyCapability(previous)
	img := image.NewNRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(x), byte(y), byte(x+y), 255
		}
	}
	for _, tc := range []struct {
		name   string
		factor float64
		fit    picture.FitMode
	}{{"full-contain", 1, picture.FitContain}, {"half-fill", .5, picture.FitFill}} {
		b.Run(tc.name, func(b *testing.B) {
			m := picture.NewWithConfig(picture.Config{CellPixelWidth: 10, CellPixelHeight: 20, KittyResolutionFactor: tc.factor, Fit: tc.fit})
			m.SetSize(128, 48)
			m.Toggle()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				frame := m.SetImage(img)().(picture.KittyFrameMsg)
				b.ReportMetric(float64(len(frame.APC)), "bytes/frame")
			}
		})
	}
}
