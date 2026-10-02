package padui

import (
	"strings"
	"testing"

	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

func TestParseKittyTransportSelectsFormatAndMedium(t *testing.T) {
	for _, tc := range []struct {
		in     string
		format picture.KittyFormat
		medium picture.KittyMedium
	}{
		{"png", picture.KittyFormatPNG, picture.KittyMediumDirect},
		{"rgba", picture.KittyFormatRGBA, picture.KittyMediumDirect},
		// Shared memory always carries RGBA; PNG is the direct fallback so a
		// failed object creation does not flood the terminal stream.
		{"shm", picture.KittyFormatPNG, picture.KittyMediumSharedMemory},
		{"SHM", picture.KittyFormatPNG, picture.KittyMediumSharedMemory},
	} {
		tr, err := ParseKittyTransport(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		// Unrelated settings must survive so pads keep their own fit and cell size.
		cfg := tr.Configure(picture.Config{CellPixelWidth: 8, Fit: picture.FitFill})
		if cfg.KittyFormat != tc.format || cfg.KittyMedium != tc.medium {
			t.Fatalf("%q: got format=%v medium=%v", tc.in, cfg.KittyFormat, cfg.KittyMedium)
		}
		if cfg.CellPixelWidth != 8 || cfg.Fit != picture.FitFill {
			t.Fatalf("%q: unrelated config changed: %+v", tc.in, cfg)
		}
	}
}

func TestParseKittyTransportRejectsUnknownValue(t *testing.T) {
	_, err := ParseKittyTransport("sharedmem")
	if err == nil {
		t.Fatal("unknown transport accepted; a typo would silently run the default")
	}
	for _, want := range []string{"sharedmem", "png", "rgba", "shm"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}
