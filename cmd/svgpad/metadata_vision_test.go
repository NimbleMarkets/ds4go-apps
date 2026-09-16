package main

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
)

func TestMetadataUserMessageWithPreview(t *testing.T) {
	msg, err := metadataUserMessage("draw a square", []byte(visualFixture), true)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role != "user" || len(msg.Parts) != 2 || msg.Parts[1].Image == nil {
		t.Fatalf("missing image: %+v", msg)
	}
	for _, want := range []string{"draw a square", visualFixture, "Describe the visible result"} {
		if !strings.Contains(msg.Parts[0].Text, want) {
			t.Errorf("input missing %q", want)
		}
	}
	img, err := png.Decode(bytes.NewReader(msg.Parts[1].Image.Data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1024 || img.Bounds().Dy() != 512 {
		t.Fatalf("wrong canvas: %v", img.Bounds())
	}
	text, err := metadataUserMessage("draw a square", []byte(visualFixture), false)
	if err != nil || len(text.Parts) != 0 || !strings.Contains(text.Content, visualFixture) {
		t.Fatalf("text-only input: %+v %v", text, err)
	}
	for _, doc := range []string{svgOpen, badColorSVG} {
		fallback, err := metadataUserMessage("draw", []byte(doc), true)
		if err == nil || len(fallback.Parts) != 0 || !strings.Contains(fallback.Content, doc) {
			t.Fatalf("render fallback: %+v %v", fallback, err)
		}
	}
}

// Run the actual enrichment command through a mock engine. Its canned answer
// has no XML metadata; reaching that parse error proves generation completed.
// The saved SVG must remain intact when enrichment cannot produce metadata.
func TestMetadataCommandUsesEngineVision(t *testing.T) {
	for _, tc := range []struct {
		name             string
		vision, glm, v41 bool
		doc              string
		wantWarning      bool
	}{
		{name: "text", doc: visualFixture},
		{name: "vision", vision: true, doc: visualFixture},
		{name: "v41 vision", vision: true, v41: true, doc: visualFixture},
		{name: "glm vision", vision: true, glm: true, doc: visualFixture},
		{name: "render fallback", vision: true, doc: badColorSVG, wantWarning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib, ctl := ds4api.NewMockLibraryWithControls()
			ctl.SetVision(tc.vision)
			ctl.SetGLM(tc.glm)
			ctl.SetDeepSeek41(tc.v41)
			eng, err := lib.NewEngine(ds4.EngineOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer eng.Close()
			path := filepath.Join(t.TempDir(), "saved.svg")
			data := []byte(tc.doc)
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			cmd := enrichMetadataCmd(context.Background(), &wg, eng, "test-model", path, "draw", data, 0, nil, "")
			// Simulate the UI reusing its source slice for the next drawing.
			for i := range data {
				data[i] = '?'
			}
			msg := cmd().(metadataDoneMsg)
			wg.Wait()
			if msg.err == nil || !strings.Contains(msg.err.Error(), "no <title>/<desc>") {
				t.Fatalf("generation did not reach metadata parser: %v", msg.err)
			}
			wantVision := tc.vision && !tc.wantWarning
			if msg.vision != wantVision || (msg.previewWarning != "") != tc.wantWarning {
				t.Fatalf("vision=%v warning=%q", msg.vision, msg.previewWarning)
			}
			if wantVision {
				if ctl.MultimodalSyncCalls() != 1 || ctl.SyncCalls() != 0 {
					t.Fatalf("sync image=%d text=%d", ctl.MultimodalSyncCalls(), ctl.SyncCalls())
				}
			} else if ctl.MultimodalSyncCalls() != 0 || ctl.SyncCalls() != 1 {
				t.Fatalf("fallback sync image=%d text=%d", ctl.MultimodalSyncCalls(), ctl.SyncCalls())
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != tc.doc {
				t.Fatal("failed metadata damaged the saved drawing")
			}
		})
	}
}

func TestMetadataVisionCancellationAndEncodingFailure(t *testing.T) {
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(ds4.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	path := filepath.Join(t.TempDir(), "saved.svg")
	if err := os.WriteFile(path, []byte(visualFixture), 0644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	msg := enrichMetadataCmd(ctx, &wg, eng, "test", path, "draw", []byte(visualFixture), 0, nil, "")().(metadataDoneMsg)
	wg.Wait()
	if !errors.Is(msg.err, context.Canceled) || ctl.MultimodalSyncCalls() != 0 {
		t.Fatalf("cancelled enrichment ran: %v", msg.err)
	}
	ctl.SetMultimodalAppendFailAt(0)
	msg = enrichMetadataCmd(context.Background(), &wg, eng, "test", path, "draw", []byte(visualFixture), 0, nil, "")().(metadataDoneMsg)
	wg.Wait()
	if msg.err == nil || ctl.MultimodalSyncCalls() != 0 {
		t.Fatalf("failed encoding reached generation: %v", msg.err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != visualFixture {
		t.Fatal("failure changed the saved SVG")
	}
}
