package web

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/library"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
)

type galleryEntry struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Saved    *time.Time `json:"saved,omitempty"`
	Kind     string     `json:"kind"`
	Controls int        `json:"controls"`
	preset   params.Preset
}

func galleryEntries(store *library.Store) ([]galleryEntry, []string, error) {
	entries := []galleryEntry{}
	var warnings []string
	seen := map[string]bool{}
	add := func(p params.Preset, saved *time.Time, kind string) {
		data, _ := json.Marshal(p)
		id := fmt.Sprintf("%x", sha256.Sum256(data))
		// Keep starters discoverable even when an identical saved version exists.
		if kind == "starter" {
			id = "starter-" + id
		}
		if seen[id] {
			return
		}
		seen[id] = true
		entries = append(entries, galleryEntry{ID: id, Name: p.Name, Saved: saved, Kind: kind, Controls: len(p.Params), preset: p})
	}
	if store != nil {
		saved, notes, err := store.List()
		// A new browser-only installation can browse starters before any archive exists.
		if err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		warnings = notes
		for _, entry := range saved {
			add(entry.Preset, &entry.Saved, "saved")
		}
	}
	for _, src := range shader.Starters() {
		values, _ := params.New(src.Params)
		add(params.Preset{Name: src.Name, Shader: src.ShadeBody, Params: src.Params, Values: values.Named()}, nil, "starter")
	}
	return entries, warnings, nil
}

func mountGallery(mux *http.ServeMux, store *library.Store, snapshot func() tools.Snapshot, fps int) {
	mux.HandleFunc("GET /api/gallery", func(w http.ResponseWriter, r *http.Request) {
		entries, warnings, err := galleryEntries(store)
		if err != nil {
			http.Error(w, "read shader gallery: "+err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Entries  []galleryEntry `json:"entries"`
			Warnings []string       `json:"warnings"`
		}{entries, warnings})
	})
	mux.HandleFunc("GET /api/gallery/{id}", func(w http.ResponseWriter, r *http.Request) {
		entries, _, err := galleryEntries(store)
		if err != nil {
			http.Error(w, "read shader gallery: "+err.Error(), 500)
			return
		}
		for _, entry := range entries {
			if entry.ID != r.PathValue("id") {
				continue
			}
			p := entry.preset
			values, _ := params.New(p.Params)
			for name, value := range p.Values {
				_, _ = values.Set(name, value)
			}
			live := snapshot()
			snap := tools.Snapshot{Source: shader.Source{Name: p.Name, ShadeBody: p.Shader, Params: p.Params}, Named: values.Named(), Width: live.Width, Height: live.Height, Time: 2, Dt: 1.0 / 60, Frame: 120}
			code, err := shader.BuildWGSL(snap.Source)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Snapshot{Snapshot: snap, WGSL: code, TargetFPS: fps})
			return
		}
		http.NotFound(w, r)
	})
}
