package padui

import (
	"fmt"
	"strings"

	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// KittyTransport is how a pad asks the picture widget to deliver Kitty frames.
// The widget reports what it actually used on every frame, because shared
// memory falls back to direct transmission when an object cannot be created.
type KittyTransport string

const (
	// KittyTransportPNG sends PNG through the terminal stream. It works
	// everywhere Kitty graphics do, including over SSH.
	KittyTransportPNG KittyTransport = "png"
	// KittyTransportRGBA sends raw pixels through the terminal stream: no
	// encode, but about 5.3 bytes per pixel on the wire.
	KittyTransportRGBA KittyTransport = "rgba"
	// KittyTransportSharedMemory passes raw pixels through a named shared
	// buffer. The terminal must be local and support Kitty's t=s medium;
	// nothing can probe that, so it is never selected automatically.
	KittyTransportSharedMemory KittyTransport = "shm"
)

// KittyTransportUsage is the flag help shared by the pads.
const KittyTransportUsage = "Kitty frame transport: png, rgba, or shm (shared memory; local terminals with Kitty t=s support only)"

// ParseKittyTransport validates a flag value, ignoring case.
func ParseKittyTransport(s string) (KittyTransport, error) {
	switch t := KittyTransport(strings.ToLower(s)); t {
	case KittyTransportPNG, KittyTransportRGBA, KittyTransportSharedMemory:
		return t, nil
	}
	return "", fmt.Errorf("unknown Kitty transport %q (want png, rgba, or shm)", s)
}

// Configure returns cfg requesting this transport, leaving other fields alone.
func (t KittyTransport) Configure(cfg picture.Config) picture.Config {
	cfg.KittyFormat, cfg.KittyMedium = picture.KittyFormatPNG, picture.KittyMediumDirect
	switch t {
	case KittyTransportRGBA:
		cfg.KittyFormat = picture.KittyFormatRGBA
	case KittyTransportSharedMemory:
		cfg.KittyMedium = picture.KittyMediumSharedMemory
	}
	return cfg
}

// FrameTransport reports what an encoded frame actually used.
func FrameTransport(frame picture.KittyFrameMsg) KittyTransport {
	switch {
	case frame.Medium == picture.KittyMediumSharedMemory:
		return KittyTransportSharedMemory
	case frame.Format == picture.KittyFormatRGBA:
		return KittyTransportRGBA
	}
	return KittyTransportPNG
}
