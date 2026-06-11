package main

import (
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
)

func TestModelDisplayNamePrefersCatalogAlias(t *testing.T) {
	m := testModel()
	m.modelPath = "/home/u/.ds4/models/ds4flash.gguf"
	if got := m.modelDisplayName(); got != "ds4flash.gguf" {
		t.Errorf("unresolved modelDisplayName() = %q, want basename fallback", got)
	}
	m.modelInfo = &ds4.ModelInfo{Alias: "q2-q4-imatrix"}
	if got := m.modelDisplayName(); got != "q2-q4-imatrix" {
		t.Errorf("modelDisplayName() = %q, want catalog alias", got)
	}
}

func TestInfoOverlayShowsCatalogMetadata(t *testing.T) {
	m := testModel()
	m.width, m.height = 110, 45
	m.modelPath = "/home/u/.ds4/models/ds4flash.gguf"
	m.modelInfo = &ds4.ModelInfo{
		Alias:    "q2-q4-imatrix",
		FileName: "DeepSeek-V4-Flash-mixed-fixed.gguf",
		SHA256:   "edabc92af63ad8b139f00087fbfc10a4072f37b7597f4fd9ad1dfa6f83002396",
		SizeGB:   98,
		Imatrix:  true,
		Notes:    "mixed q2/q4 imatrix: q2 routed experts with last 6 layers q4",
		Default:  true,
	}

	out := m.infoOverlay()
	for _, want := range []string{
		"q2-q4-imatrix",
		"edabc92af63a", // truncated sha256
		"DeepSeek-V4-Flash-mixed-fixed.gguf",
		"98 GB",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("info overlay missing %q", want)
		}
	}
}

func TestInfoOverlayWithoutCatalogInfo(t *testing.T) {
	m := testModel()
	m.width, m.height = 110, 45
	m.modelPath = "/somewhere/custom-quant.gguf"

	out := m.infoOverlay()
	if !strings.Contains(out, "custom-quant.gguf") {
		t.Error("info overlay missing file basename fallback")
	}
	if strings.Contains(out, "sha256") {
		t.Error("info overlay shows catalog rows for an unresolved model")
	}
}
