package padui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// FrameEncodedMsg wraps the picture widget's encoded frame with what it cost.
// Forward Msg to picture.Model.Update as usual; Encode covers prepare + PNG +
// base64 + escape framing, and Bytes is what will be written to the terminal.
// With shared memory Bytes is only the reference; the pixels bypass the stream.
// The GPU or CPU render is cheap for most pads; encode and transfer are where
// frame time goes, so pads should show these next to the raster size.
type FrameEncodedMsg struct {
	Msg    tea.Msg
	Encode time.Duration
	Bytes  int
	Kitty  bool
	// Transport is what this frame actually used, after any fallback.
	Transport KittyTransport
}

// TimeEncode wraps a picture SetImage command so its result reports encode
// time and payload size. A nil command (glyph mode, nothing to encode) stays nil.
func TimeEncode(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		start := time.Now()
		msg := cmd()
		out := FrameEncodedMsg{Msg: msg, Encode: time.Since(start)}
		if frame, ok := msg.(picture.KittyFrameMsg); ok {
			out.Bytes = len(frame.APC)
			out.Kitty = true
			out.Transport = FrameTransport(frame)
		}
		return out
	}
}
