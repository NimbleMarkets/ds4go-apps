package main

import (
	"strings"
	"testing"
)

func TestChunkFromArgs(t *testing.T) {
	cases := []struct {
		name, args, want string
	}{
		{"simple", `{"chunk":"<rect/>"}`, "<rect/>"},
		{"escapes", `{"chunk":"<text>a\"b</text>"}`, `<text>a"b</text>`},
		{"unicode escapes", `{"chunk":"<rect/>"}`, "<rect/>"},
		{"empty chunk", `{"chunk":""}`, ""},
		{"wrong key", `{"data":"<rect/>"}`, ""},
		{"invalid json", `{"chunk":`, ""},
		{"empty args", ``, ""},
	}
	for _, tc := range cases {
		if got := chunkFromArgs(tc.args); got != tc.want {
			t.Errorf("%s: chunkFromArgs(%q) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestPreviewSVG(t *testing.T) {
	base := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10">`
	got := previewSVG(base, []string{`<rect width="5"/>`, `<circle r="2"/>`})
	want := base + `<rect width="5"/>` + `<circle r="2"/>` + "</svg>"
	if string(got) != want {
		t.Errorf("previewSVG = %q, want %q", got, want)
	}

	// Already-closed documents are not double-closed.
	closed := previewSVG(base+`<rect/></svg>`, nil)
	if strings.Count(string(closed), "</svg>") != 1 {
		t.Errorf("previewSVG double-closed: %q", closed)
	}

	// No <svg root yet -> nothing to preview.
	if got := previewSVG("", nil); got != nil {
		t.Errorf("previewSVG(empty) = %q, want nil", got)
	}
	if got := previewSVG("thinking about it", []string{"text"}); got != nil {
		t.Errorf("previewSVG(no svg root) = %q, want nil", got)
	}
}
