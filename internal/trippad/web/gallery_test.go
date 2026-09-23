package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/library"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
)

func TestGalleryLoadsSavedValuesAndExactShader(t *testing.T) {
	store, err := library.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := shader.Starters()[0]
	src.Name = "A saved version <script>"
	p := params.Preset{Name: src.Name, Shader: src.ShadeBody, Params: src.Params, Values: map[string]float32{src.Params[0].Name: src.Params[0].Max}}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "broken.trip.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := Handler(func() tools.Snapshot { return tools.Snapshot{Source: src, Width: 800, Height: 480} }, 30, true, store)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost/api/gallery", nil))
	var list struct {
		Entries  []galleryEntry
		Warnings []string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(list.Entries) != len(shader.Starters())+1 || len(list.Warnings) != 1 {
		t.Fatalf("wrong listing: %d %+v", rec.Code, list)
	}
	entry := list.Entries[0]
	if entry.Name != src.Name || entry.Saved == nil || entry.Kind != "saved" || len(entry.ID) != 64 {
		t.Fatalf("wrong saved metadata: %+v", entry)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost/api/gallery/"+entry.ID, nil))
	var got Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want, _ := shader.BuildWGSL(src)
	if rec.Code != 200 || got.WGSL != want || got.Named[src.Params[0].Name] != src.Params[0].Max || got.Named[src.Params[1].Name] != src.Params[1].Default || got.Width != 800 || got.Time != 2 || got.Native {
		t.Fatalf("wrong loaded preset: %+v", got)
	}
	for _, path := range []string{"/api/gallery/not-an-id", "/api/gallery/broken.trip.json"} {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost"+path, nil))
		if rec.Code != 404 {
			t.Fatalf("unexpected file access %s: %d", path, rec.Code)
		}
	}
}

func TestGalleryWithoutDirectoryHasStartersAndDoesNotCreateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	entries, warnings, err := galleryEntries(&library.Store{Dir: dir})
	if err != nil || len(warnings) != 0 || len(entries) != len(shader.Starters()) {
		t.Fatalf("starters: %d %v %v", len(entries), warnings, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("browser gallery created directory")
	}
}

func TestSavedStarterDoesNotHideBuiltin(t *testing.T) {
	store, err := library.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := shader.Starters()[0]
	values, _ := params.New(src.Params)
	if err := store.Save(params.Preset{Name: src.Name, Shader: src.ShadeBody, Params: src.Params, Values: values.Named()}); err != nil {
		t.Fatal(err)
	}
	entries, _, err := galleryEntries(store)
	if err != nil {
		t.Fatal(err)
	}
	starters := 0
	for _, entry := range entries {
		if entry.Kind == "starter" {
			starters++
		}
	}
	if starters != len(shader.Starters()) || len(entries) != starters+1 {
		t.Fatal("saved copy hides starter filter")
	}
}
