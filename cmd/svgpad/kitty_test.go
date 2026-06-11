package main

import (
	"testing"

	svg "github.com/NimbleMarkets/ntcharts-svg/svg"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
)

// withKittyCapability forces the process-wide Kitty capability for the
// duration of a test, restoring the prior value afterwards.
func withKittyCapability(t *testing.T, c picture.KittyCapability) {
	t.Helper()
	prev := picture.KittySupported()
	picture.ForceKittyCapability(c)
	t.Cleanup(func() { picture.ForceKittyCapability(prev) })
}

// TestAutoEnableKittySwitchesFromGlyph verifies the startup auto-toggle
// moves the widget from glyph to Kitty rendering once the probe has
// confirmed support.
func TestAutoEnableKittySwitchesFromGlyph(t *testing.T) {
	withKittyCapability(t, picture.KittyCapabilitySupported)
	m := &model{svgWidget: svg.New(80, 24)}

	m.autoEnableKittyCmd()

	if got := m.svgWidget.RenderMode(); got != svg.RenderKitty {
		t.Errorf("RenderMode = %v, want RenderKitty", got)
	}
}

// TestAutoEnableKittyRespectsUnsupported verifies the auto-toggle leaves
// glyph mode alone when the terminal does not support Kitty graphics.
func TestAutoEnableKittyRespectsUnsupported(t *testing.T) {
	withKittyCapability(t, picture.KittyCapabilityUnsupported)
	m := &model{svgWidget: svg.New(80, 24)}

	m.autoEnableKittyCmd()

	if got := m.svgWidget.RenderMode(); got != svg.RenderGlyph {
		t.Errorf("RenderMode = %v, want RenderGlyph", got)
	}
}

// TestAutoEnableKittyIsOneShot verifies a manual toggle back to glyph
// sticks: the auto-enable check must not fire again after the user has
// switched modes.
func TestAutoEnableKittyKeepsKittyMode(t *testing.T) {
	withKittyCapability(t, picture.KittyCapabilitySupported)
	m := &model{svgWidget: svg.New(80, 24)}
	m.svgWidget.ToggleRenderMode() // already in Kitty mode

	m.autoEnableKittyCmd()

	if got := m.svgWidget.RenderMode(); got != svg.RenderKitty {
		t.Errorf("RenderMode = %v, want RenderKitty", got)
	}
}
