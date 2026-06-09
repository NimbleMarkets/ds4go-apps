package appinit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/spf13/pflag"
)

func TestRegisterFlagsPerAppDefaults(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	f := RegisterFlags(fs, "cadpad", Defaults{Ctx: 16384, Power: 80})
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if f.Ctx != 16384 {
		t.Errorf("Ctx = %d, want 16384", f.Ctx)
	}
	if f.Power != 80 {
		t.Errorf("Power = %d, want 80", f.Power)
	}
	if f.MTP != "none" {
		t.Errorf("MTP = %q, want \"none\"", f.MTP)
	}
}

func TestRegisterFlagsFallbackDefaults(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	f := RegisterFlags(fs, "glyphpad", Defaults{})
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if f.Ctx != 32768 {
		t.Errorf("Ctx = %d, want 32768", f.Ctx)
	}
	if f.Power != 100 {
		t.Errorf("Power = %d, want 100", f.Power)
	}
}

func TestRegisterFlagsParse(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	f := RegisterFlags(fs, "svgpad", Defaults{})
	err := fs.Parse([]string{"-m", "model.gguf", "--ctx", "8192", "-d", "--backend", "cpu", "--lib", "libds4.dylib", "--mtp", "", "--power", "50"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Model != "model.gguf" || f.Ctx != 8192 || !f.Debug || f.Backend != "cpu" ||
		f.Lib != "libds4.dylib" || f.MTP != "" || f.Power != 50 {
		t.Errorf("parsed Flags = %+v", f)
	}
}

func TestSelectBackendExplicit(t *testing.T) {
	cases := map[string]ds4.Backend{
		"cpu":   ds4.BackendCPU,
		"cuda":  ds4.BackendCUDA,
		"metal": ds4.BackendMetal,
	}
	for name, want := range cases {
		if got := selectBackend(name, ""); got != want {
			t.Errorf("selectBackend(%q) = %v, want %v", name, got, want)
		}
	}
	// Empty/unknown names take the ds4.DetectDefaultBackend path; not
	// exercised here to avoid requiring a local libds4.
}

func TestResolveMTP(t *testing.T) {
	if got := resolveMTP("none"); got != "" {
		t.Errorf(`resolveMTP("none") = %q, want ""`, got)
	}
	if got := resolveMTP(""); got != ds4.DefaultMTPPath() {
		t.Errorf(`resolveMTP("") = %q, want default`, got)
	}
	valid := filepath.Join(t.TempDir(), "mtp.gguf")
	if err := os.WriteFile(valid, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := resolveMTP(valid); got != valid {
		t.Errorf("resolveMTP(valid) = %q, want %q", got, valid)
	}
	missing := filepath.Join(t.TempDir(), "missing.gguf")
	if got := resolveMTP(missing); got != ds4.DefaultMTPPath() {
		t.Errorf("resolveMTP(missing) = %q, want default", got)
	}
	if got := resolveMTP(t.TempDir()); got != ds4.DefaultMTPPath() {
		t.Errorf("resolveMTP(dir) = %q, want default", got)
	}
	empty := filepath.Join(t.TempDir(), "empty.gguf")
	if err := os.WriteFile(empty, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if got := resolveMTP(empty); got != ds4.DefaultMTPPath() {
		t.Errorf("resolveMTP(empty file) = %q, want default", got)
	}
}

func TestBootstrapRejectsBadPower(t *testing.T) {
	for _, p := range []int{0, 101, -1} {
		f := &Flags{app: "test", Power: p, Backend: "cpu", MTP: "none"}
		if _, err := Bootstrap(f, WithoutEngine(true)); err == nil {
			t.Errorf("Bootstrap with power=%d: want error, got nil", p)
		}
	}
}

func TestBootstrapNoEngine(t *testing.T) {
	t.Chdir(t.TempDir())
	f := &Flags{app: "testapp", Backend: "cpu", MTP: "none", Power: 100, Ctx: 4096}
	app, err := Bootstrap(f, WithoutEngine(true))
	if err != nil {
		t.Fatal(err)
	}
	if app.Lib != nil {
		t.Error("Lib: want nil in no-engine mode")
	}
	if app.Name != "testapp" {
		t.Errorf("Name = %q, want testapp", app.Name)
	}
	if app.EngineOpts.PowerPercent != 100 || !app.EngineOpts.WarmWeights {
		t.Errorf("EngineOpts = %+v", app.EngineOpts)
	}
	if app.EngineOpts.MTPPath != "" {
		t.Errorf("MTPPath = %q, want empty for --mtp none", app.EngineOpts.MTPPath)
	}
	if app.EngineOpts.Backend != ds4.BackendCPU {
		t.Errorf("Backend = %v, want BackendCPU", app.EngineOpts.Backend)
	}
	if app.LogBuf == nil || app.Logger == nil {
		t.Fatal("LogBuf and Logger must be non-nil")
	}

	app.Close(errors.New("boom"))

	data, err := os.ReadFile("testapp.log")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"=== testapp start", "backend=cpu", "error: boom", "=== testapp end ==="} {
		if !strings.Contains(s, want) {
			t.Errorf("testapp.log missing %q\nlog:\n%s", want, s)
		}
	}
}

func TestCloseOnLiteralApp(t *testing.T) {
	// Tests construct App values directly (see cmd/cadpad tests); Close must
	// be safe with nil internals.
	app := &App{Name: "lit"}
	app.Close(nil)
	app.Close(errors.New("x"))
}
