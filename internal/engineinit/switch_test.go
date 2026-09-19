package engineinit

import (
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"os"
	"path/filepath"
	"testing"
)

func TestModelOptionsPreserveRuntimeAndClearCompanions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.gguf")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	base := ds4.EngineOptions{ModelPath: "old", VisionPath: "oldvision", MTPPath: "oldmtp", SSDStreaming: true, Backend: ds4.BackendMetal, PowerPercent: 70, ContextSize: 4096}
	opts, err := ModelOptions(base, ds4.ModelInfo{Alias: "custom", Path: path, Installed: true}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if opts.ModelPath != path || opts.VisionPath != "" || opts.MTPPath != "" || !opts.SSDStreaming || opts.Backend != base.Backend || opts.PowerPercent != 70 || opts.ContextSize != 4096 {
		t.Fatal("runtime settings lost or companions leaked")
	}
	if _, err := ModelOptions(base, ds4.ModelInfo{Path: "missing", Installed: true}, false, false); err == nil {
		t.Fatal("missing checkpoint accepted")
	}
}
func TestSwitchClosesOldSessionEvenWhenReplacementFails(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	engine, err := lib.NewEngine(ds4.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := engine.NewSession(4096)
	if err != nil {
		t.Fatal(err)
	}
	result := Switch(nil, engine, session, ds4.EngineOptions{}, 4096)
	if result.Err == nil {
		t.Fatal("missing library succeeded")
	}
	if err := session.Sync([]int{1}); err == nil {
		t.Fatal("previous session remains open")
	}
	if _, err := engine.NewSession(256); err == nil {
		t.Fatal("previous engine remains open")
	}
}
