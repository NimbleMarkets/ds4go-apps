package tools

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/library"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
)

func TestArchivePreservesEveryCompiledVersionAndControls(t *testing.T) {
	s, renderer := testState(t)
	store, _ := library.New(t.TempDir())
	s.SetArchive(store.Save)
	s.Set("warp", 1.5)
	before := s.Snapshot()
	next := shader.Starters()[1]
	if err := s.Replace(next, nil, before.Revision); err != nil {
		t.Fatal(err)
	}
	entries, _, err := store.List()
	if err != nil || len(entries) != 2 {
		t.Fatal("replacement didn't archive old and new shaders")
	}
	var oldPath string
	for _, e := range entries {
		if e.Preset.Name == before.Source.Name {
			oldPath = e.Path
			if e.Preset.Values["warp"] != 1.5 {
				t.Fatal("lost tuned controls")
			}
		}
	}
	renderer.err = errors.New("compile failure")
	if err := s.Replace(shader.Starters()[2], nil, s.Snapshot().Revision); err == nil {
		t.Fatal("accepted invalid shader")
	}
	entries, _, _ = store.List()
	if len(entries) != 2 {
		t.Fatal("failed compilation entered history")
	}
	renderer.err = nil
	if err := s.Load(oldPath); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Snapshot().Source, before.Source) || s.Snapshot().Named["warp"] != 1.5 {
		t.Fatal("gallery restore lost source or controls")
	}
	entries, _, _ = store.List()
	if len(entries) != 2 {
		t.Fatal("restore duplicated history")
	}
}

func TestArchiveFailureLeavesWorkingShaderUntouched(t *testing.T) {
	s, _ := testState(t)
	before := s.Snapshot()
	s.SetArchive(func(params.Preset) error { return errors.New("disk full") })
	err := s.Replace(shader.Starters()[1], nil, before.Revision)
	if err == nil || !strings.Contains(err.Error(), "disk full") || !reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatal("archive failure wasn't reported without replacing working state")
	}
}
