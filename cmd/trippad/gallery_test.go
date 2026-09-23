package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/library"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
)

func TestGalleryBrowsePreviewAndRestorePreservePrompt(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 35
	m.library, _ = library.New(t.TempDir())
	m.state.SetArchive(m.library.Save)
	m.state.Set("warp", 1.5)
	before := m.state.Snapshot()
	if err := m.state.Replace(shader.Starters()[1], nil, before.Revision); err != nil {
		t.Fatal(err)
	}
	working := m.state.Snapshot()
	m.input.Focus()
	m.input.SetValue("keep my prompt")
	m.input.SetCursor(4)
	_, load := m.Update(tea.KeyPressMsg{Code: tea.KeyF3})
	if load == nil || !m.gallery.open {
		t.Fatal("F3 didn't open gallery from prompt")
	}
	_, preview := m.Update(load())
	if preview == nil {
		t.Fatal("gallery didn't request preview")
	}
	m.Update(preview())
	if m.gallery.image == nil || !reflect.DeepEqual(working, m.state.Snapshot()) {
		t.Fatal("browsing changed live shader or didn't render a preview")
	}
	_, preview = m.Update(tea.KeyPressMsg{Code: 'p', Text: "plasma"})
	if preview == nil {
		t.Fatal("selection didn't request a preview")
	}
	m.Update(preview())
	if len(m.galleryMatches()) != 1 || !strings.Contains(m.View().Content, "plasma") {
		t.Fatal("gallery filter failed")
	}
	_, restore := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if restore == nil || m.gallery.open {
		t.Fatal("Enter didn't load selected shader")
	}
	m.Update(restore())
	if !reflect.DeepEqual(m.state.Snapshot().Source, before.Source) || m.state.Snapshot().Named["warp"] != 1.5 {
		t.Fatal("restore lost shader or controls")
	}
	if m.input.Value() != "keep my prompt" || m.input.Position() != 4 || !m.input.Focused() {
		t.Fatal("gallery lost prompt/focus")
	}
}

func TestGalleryStaleLoadsAndBusyRestore(t *testing.T) {
	m := testModel(t)
	m.library, _ = library.New(t.TempDir())
	m.state.SetArchive(m.library.Save)
	old := m.openGallery()
	m.galleryKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(old())
	if m.gallery.open || m.gallery.preview != nil {
		t.Fatal("late list result reopened dismissed gallery")
	}
	fresh := m.openGallery()
	_, preview := m.Update(fresh())
	m.Update(preview())
	m.gen, _ = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) { defer close(ch); <-ctx.Done() })
	if m.galleryKey(tea.KeyPressMsg{Code: tea.KeyEnter}) != nil || !m.gallery.open || !strings.Contains(m.gallery.note, "Wait") {
		t.Fatal("restore allowed during generation")
	}
	m.gen.StopAndWait()
	m.gen = nil
	m.galleryKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.gallery.open {
		t.Fatal("can't dismiss gallery")
	}
}
