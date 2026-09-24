package padui

import (
	"fmt"
	"image"
	"math"
	"testing"

	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

func kittyPicture(t testing.TB, cols, rows, cw, ch int) *picture.Model {
	t.Helper()
	previous := picture.KittySupported()
	picture.ForceKittyCapability(picture.KittyCapabilitySupported)
	t.Cleanup(func() { picture.ForceKittyCapability(previous) })
	model := picture.NewWithConfig(picture.Config{CellPixelWidth: cw, CellPixelHeight: ch, Fit: picture.FitFill})
	pic := &model
	pic.SetSize(cols, rows)
	pic.Toggle()
	if pic.Mode() != picture.PictureKitty {
		t.Fatal("picture not in Kitty mode")
	}
	return pic
}

// smoothFrame resembles shader output: gradients that PNG compresses well but
// not trivially, so encode timings are representative.
func smoothFrame(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			u, v := float64(x)/float64(w)*6, float64(y)/float64(h)*6
			i := img.PixOffset(x, y)
			img.Pix[i] = byte(127 + 63*(math.Sin(u+v)+math.Sin(u*1.3-v*0.7)))
			img.Pix[i+1] = byte(127 + 127*math.Sin(u*0.9+v*1.7+1))
			img.Pix[i+2] = byte(127 + 127*math.Cos(u*1.1-v*1.3))
			img.Pix[i+3] = 255
		}
	}
	return img
}

func TestTimeEncodeReportsDurationAndBytes(t *testing.T) {
	if TimeEncode(nil) != nil {
		t.Fatal("nil command must stay nil so glyph mode skips the wrapper")
	}
	pic := kittyPicture(t, 4, 3, 8, 16)
	cmd := TimeEncode(pic.SetImage(smoothFrame(32, 48)))
	if cmd == nil {
		t.Fatal("missing encode command")
	}
	msg, ok := cmd().(FrameEncodedMsg)
	if !ok {
		t.Fatalf("got %T, want FrameEncodedMsg", cmd())
	}
	frame, ok := msg.Msg.(picture.KittyFrameMsg)
	if !ok || !msg.Kitty || msg.Bytes != len(frame.APC) || msg.Bytes == 0 || msg.Encode < 0 {
		t.Fatalf("bad timing record: %+v", msg)
	}
}

// BenchmarkKittyEncode measures the picture widget's per-frame Kitty encode
// (prepare + PNG + base64 + APC framing) at typical pane sizes. Run with
// go test ./internal/padui -bench KittyEncode -run ^$ -benchmem
func BenchmarkKittyEncode(b *testing.B) {
	for _, sz := range [][2]int{{640, 480}, {1134, 756}, {1280, 960}} {
		b.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(b *testing.B) {
			pic := kittyPicture(b, sz[0]/8, sz[1]/16, 8, 16)
			img := smoothFrame(sz[0], sz[1])
			var bytes int
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				msg := TimeEncode(pic.SetImage(img))().(FrameEncodedMsg)
				bytes = msg.Bytes
			}
			b.ReportMetric(float64(bytes)/1024, "KB/frame")
		})
	}
}
