package appinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
)

func TestDeferredStartupModelSelection(t *testing.T) {
	t.Setenv("DS4_DIR", t.TempDir())
	for _, value := range []string{"", "not-installed", filepath.Join(t.TempDir(), "missing.gguf")} {
		if _, err := resolveStartupModel(value, false); err == nil {
			t.Fatal("ordinary startup should reject missing model")
		}
		path, err := resolveStartupModel(value, true)
		if err != nil || path != "" {
			t.Fatalf("picker startup: path=%q error=%v", path, err)
		}
	}
	path := filepath.Join(t.TempDir(), "custom.gguf")
	if err := os.WriteFile(path, []byte("model"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveStartupModel(path, true); err != nil || got != path {
		t.Fatalf("valid model changed: %q %v", got, err)
	}
}

func TestResolveInstalledModelAliasAndVisionCompanion(t *testing.T) {
	t.Setenv("DS4_DIR", t.TempDir())
	if err := os.MkdirAll(ds4.DefaultModelsDir(), 0755); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(ds4.DefaultModelsDir(), "DeepSeek-V4-Flash-Vision-Exp-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8.gguf")
	encoder := filepath.Join(ds4.DefaultModelsDir(), "DeepSeek-V4-Flash-Vision-Encoder.gguf")
	for _, p := range []string{model, encoder} {
		if err := os.WriteFile(p, []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := resolveModelPath("vision-q2")
	if err != nil || got != model {
		t.Fatalf("path=%q err=%v", got, err)
	}
	opts := ds4.EngineOptions{ModelPath: got}
	ds4.ApplyVisionDefaults(&opts)
	if opts.VisionPath != encoder {
		t.Fatalf("encoder=%q", opts.VisionPath)
	}
	if err := os.Link(model, ds4.DefaultModelPath()); err != nil {
		t.Fatal(err)
	}
	got, err = resolveModelPath("")
	if err != nil || got != ds4.DefaultModelPath() {
		t.Fatalf("default=%q err=%v", got, err)
	}
}

func TestResolveModelPathsAndErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DS4_DIR", t.TempDir())
	for _, name := range []string{"custom.gguf", "vision-q2"} {
		if err := os.WriteFile(name, []byte("custom model"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := resolveModelPath(name)
		if err != nil || got != name {
			t.Fatalf("existing file %q: %q %v", name, got, err)
		}
	}
	if err := os.WriteFile("empty.gguf", nil, 0644); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "not-installed", "missing.gguf", "./missing", "empty.gguf", "."} {
		_, err := resolveModelPath(value)
		if err == nil {
			t.Fatalf("accepted %q", value)
		}
		if strings.Contains(err.Error(), "q2-imatrix") {
			t.Fatalf("unrelated download recommendation: %v", err)
		}
	}
}

func TestResolveModelAliasDiagnostics(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DS4_DIR", t.TempDir())
	for _, tc := range []struct{ alias, want string }{
		{"vision-q2", "Run: ds4go model download vision-q2"},
		{"made-up-model", `unknown model alias "made-up-model"`},
	} {
		_, err := resolveModelPath(tc.alias)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("resolve %q: %v, want %q", tc.alias, err, tc.want)
		}
	}
}
