package main

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"reflect"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/NimbleMarkets/ds4go-apps/internal/bubble"
	"github.com/NimbleMarkets/ds4go-apps/internal/padui"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/library"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
	"github.com/NimbleMarkets/ntcharts/v2/picture"
	"github.com/charmbracelet/x/ansi"
)

type shaderGallery struct {
	open, loading  bool
	request        uint64
	entries        []library.Entry
	query, note    string
	cursor         int
	preview        *bubble.Generation
	previewPath    string
	image          *image.NRGBA
	previewError   string
	thumbnail      string
	thumbW, thumbH int
}

type galleryLoadedMsg struct {
	request  uint64
	entries  []library.Entry
	warnings []string
	err      error
}
type galleryPreviewMsg struct {
	path string
	img  *image.NRGBA
	err  error
}

func (m *model) openGallery() tea.Cmd {
	if m.library == nil {
		m.status = "Shader gallery is unavailable"
		return nil
	}
	m.gallery.open, m.gallery.loading = true, true
	m.gallery.query, m.gallery.note, m.gallery.cursor = "", "", 0
	m.gallery.image, m.gallery.previewError = nil, ""
	m.gallery.request++
	m.showLog, m.inspect.kind = false, ""
	request, store, state := m.gallery.request, m.library, m.state
	return func() tea.Msg {
		if err := state.Checkpoint(); err != nil {
			return galleryLoadedMsg{request: request, err: err}
		}
		entries, warnings, err := store.List()
		return galleryLoadedMsg{request, entries, warnings, err}
	}
}

func (m *model) galleryMatches() []library.Entry {
	var out []library.Entry
	query := strings.ToLower(strings.TrimSpace(m.gallery.query))
	for _, e := range m.gallery.entries {
		if strings.Contains(strings.ToLower(e.Preset.Name+" "+e.Saved.Local().Format("2006-01-02 15:04:05")+" "+filepath.Base(e.Path)), query) {
			out = append(out, e)
		}
	}
	return out
}

func (m *model) selectedGalleryEntry() (library.Entry, bool) {
	entries := m.galleryMatches()
	if len(entries) == 0 || m.gallery.loading {
		return library.Entry{}, false
	}
	return entries[min(max(0, m.gallery.cursor), len(entries)-1)], true
}

func (m *model) galleryPreview() tea.Cmd {
	e, ok := m.selectedGalleryEntry()
	if !m.gallery.open || !ok || m.gallery.preview != nil {
		return nil
	}
	m.gallery.previewPath = e.Path
	m.gallery.image, m.gallery.previewError = nil, ""
	state := m.state
	var cmd tea.Cmd
	m.gallery.preview, cmd = bubble.Start(func(ctx context.Context, ch chan<- tea.Msg) {
		defer close(ch)
		values, err := params.New(e.Preset.Params)
		var img *image.NRGBA
		if err == nil {
			for k, v := range e.Preset.Values {
				if _, err = values.Set(k, v); err != nil {
					break
				}
			}
		}
		if err == nil && ctx.Err() == nil {
			img, err = state.Render(tools.Snapshot{Source: shader.Source{Name: e.Preset.Name, ShadeBody: e.Preset.Shader, Params: e.Preset.Params}, Values: values.Values(), Time: 2, Dt: 1.0 / 60, Frame: 120}, 240, 144)
		}
		// Always publish completion so navigation can schedule the newest
		// selection. There is at most one GPU preview job in flight.
		ch <- galleryPreviewMsg{path: e.Path, img: img, err: err}
	})
	return cmd
}

func (m *model) galleryKey(msg tea.KeyPressMsg) tea.Cmd {
	before, _ := m.selectedGalleryEntry()
	switch msg.String() {
	case "esc", "f3":
		m.gallery.open = false
		return nil
	case "up":
		m.gallery.cursor--
	case "down":
		m.gallery.cursor++
	case "pgup":
		m.gallery.cursor -= 5
	case "pgdown":
		m.gallery.cursor += 5
	case "backspace":
		runes := []rune(m.gallery.query)
		if len(runes) > 0 {
			m.gallery.query = string(runes[:len(runes)-1])
		}
		m.gallery.cursor = 0
	case "ctrl+u":
		m.gallery.query, m.gallery.cursor = "", 0
	case "enter":
		if m.gen != nil || m.action != nil {
			m.gallery.note = "Wait for generation or preset loading to finish before restoring a shader"
			return nil
		}
		e, ok := m.selectedGalleryEntry()
		if !ok {
			return nil
		}
		m.gallery.open = false
		state := m.state
		// The restored shader is authoritative for the next request; don't
		// carry old tool replies/images claiming a different shader is live.
		m.history = checkpointHistory(m.history)
		return m.runAction("Loaded gallery shader "+e.Preset.Name, func() error { return state.Load(e.Path) })
	default:
		if msg.Text != "" && len([]rune(m.gallery.query)) < 256 {
			m.gallery.query += msg.Text
			m.gallery.cursor = 0
		}
	}
	m.gallery.cursor = min(max(0, m.gallery.cursor), max(0, len(m.galleryMatches())-1))
	after, _ := m.selectedGalleryEntry()
	if before.Path != after.Path {
		m.gallery.image, m.gallery.previewError = nil, ""
		return m.galleryPreview()
	}
	return nil
}

func (m *model) galleryView() string {
	entries := m.galleryMatches()
	w := max(1, min(84, m.width-8))
	previewRows := max(2, min(10, (m.height-12)/2))
	rows := max(1, m.height-12-previewRows)
	lines := []string{"Search: " + cleanInspectText(m.gallery.query), fmt.Sprintf("%d saved versions · source + controls · newest first", len(entries)), ""}
	if m.gallery.loading {
		lines = append(lines, "Loading shader history…")
	} else if len(entries) == 0 {
		lines = append(lines, "No saved shaders match.")
	}
	snap := m.state.Snapshot()
	start := max(0, m.gallery.cursor-rows+1)
	for i := start; i < min(len(entries), start+rows); i++ {
		e := entries[i]
		mark := "  "
		if i == m.gallery.cursor {
			mark = "> "
		}
		current := ""
		if e.Preset.Name == snap.Source.Name && e.Preset.Shader == snap.Source.ShadeBody && reflect.DeepEqual(e.Preset.Params, snap.Source.Params) && reflect.DeepEqual(e.Preset.Values, snap.Named) {
			current = " [current]"
		}
		line := fmt.Sprintf("%s%s · %s · %.8s%s", mark, e.Saved.Local().Format("Jan 02 15:04:05"), e.Preset.Name, filepath.Base(e.Path), current)
		lines = append(lines, ansi.Truncate(strings.Join(strings.Fields(cleanInspectText(line)), " "), w, "…"))
	}
	lines = append(lines, "", "Preview · 2 seconds")
	if m.gallery.image != nil {
		if m.gallery.thumbnail == "" || m.gallery.thumbW != w || m.gallery.thumbH != previewRows {
			pic := picture.NewWithConfig(picture.Config{Fit: picture.FitContain})
			pic.SetSize(min(40, w), previewRows)
			pic.SetImage(m.gallery.image)
			m.gallery.thumbnail = pic.View().Content
			m.gallery.thumbW, m.gallery.thumbH = w, previewRows
		}
		lines = append(lines, strings.Split(m.gallery.thumbnail, "\n")...)
	} else if m.gallery.previewError != "" {
		lines = append(lines, ansi.Truncate(cleanInspectText(m.gallery.previewError), w, "…"))
	} else if len(entries) > 0 {
		lines = append(lines, "Rendering preview…")
	}
	hint := "Type to search · ↑↓ select · Enter load · Esc / F3 close"
	if m.gallery.note != "" {
		hint = cleanInspectText(m.gallery.note)
	}
	return padui.Dialog(m.width, m.height, "Shader gallery · F3", lines, 0, hint)
}
