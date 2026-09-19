package engineinit

import (
	"fmt"
	ds4 "github.com/NimbleMarkets/ds4go"
	"os"
)

// ModelOptions preserves runtime settings while rebinding checkpoint-specific
// companions. The host opts into vision only after it can consume image input.
func ModelOptions(base ds4.EngineOptions, info ds4.ModelInfo, mtp, vision bool) (ds4.EngineOptions, error) {
	if !info.Installed || !info.IsChatModel() {
		return base, fmt.Errorf("choose an installed chat model")
	}
	st, err := os.Stat(info.Path)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return base, fmt.Errorf("model missing or empty: %s", info.Path)
	}
	base.ModelPath = info.Path
	base.MTPPath = ""
	base.VisionPath = ""
	if mtp {
		ds4.ApplyMTPDefaults(&base)
	}
	if vision {
		ds4.ApplyVisionDefaults(&base)
	}
	return base, nil
}

// Switch closes the previous session and engine before opening a replacement.
// The caller must first stop all work using the previous engine.
func Switch(lib *ds4.Library, engine *ds4.Engine, session *ds4.Session, opts ds4.EngineOptions, ctxSize int) Result {
	if session != nil {
		session.Close()
	}
	if engine != nil {
		engine.Close()
	}
	return Open(lib, opts, ctxSize)
}
