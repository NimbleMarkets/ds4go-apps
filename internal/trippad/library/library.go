// Package library stores immutable, content-addressed shader presets.
package library

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
)

type Entry struct {
	Path   string
	Saved  time.Time
	Preset params.Preset
}

type Store struct {
	Dir string
	mu  sync.Mutex
}

func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("gallery directory is required")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

// Save keeps one file per distinct source, definitions and parameter values.
// Reopening or saving the same preset does not create another revision.
func (s *Store) Save(p params.Preset) error {
	if err := p.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, fmt.Sprintf("%x.trip.json", sha256.Sum256(data)))
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := params.Load(path); err == nil {
		b, _ := json.Marshal(existing)
		if string(b) != string(data) {
			return fmt.Errorf("gallery entry changed unexpectedly: %s", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return params.Save(path, p)
}

// List returns newest first. Damaged files are reported without hiding the
// healthy entries; callers can still browse and restore the remaining work.
func (s *Store) List() ([]Entry, []string, error) {
	files, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, nil, err
	}
	var entries []Entry
	var warnings []string
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".trip.json") {
			continue
		}
		path := filepath.Join(s.Dir, file.Name())
		info, err := file.Info()
		if err != nil {
			warnings = append(warnings, file.Name()+": "+err.Error())
			continue
		}
		if !info.Mode().IsRegular() || info.Size() > 4<<20 {
			warnings = append(warnings, file.Name()+": not a regular preset under 4 MiB")
			continue
		}
		p, err := params.Load(path)
		if err != nil {
			warnings = append(warnings, file.Name()+": "+err.Error())
			continue
		}
		entries = append(entries, Entry{Path: path, Saved: info.ModTime(), Preset: p})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Saved.Equal(entries[j].Saved) {
			return entries[i].Path < entries[j].Path
		}
		return entries[i].Saved.After(entries[j].Saved)
	})
	return entries, warnings, nil
}
