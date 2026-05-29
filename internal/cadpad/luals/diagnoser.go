package luals

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"github.com/NimbleMarkets/ds4go/lsp"
)

// DefaultTimeout bounds each WaitForDiagnostics call.
const DefaultTimeout = 5 * time.Second

// Diagnoser owns one persistent lua-language-server and reports diagnostics for
// workspace-relative .lua paths. A nil *Diagnoser is a valid no-op.
type Diagnoser struct {
	client  *lsp.Client
	timeout time.Duration

	mu     sync.Mutex
	opened map[string]bool // uri -> didOpen already sent
}

// New starts lua-language-server rooted at workspace, loading defsDir (where
// WriteDefs put sdf.lua) as a library so the sdf API resolves. It returns
// (nil, nil) when lua-language-server is not on PATH — the caller treats a nil
// Diagnoser as "diagnostics disabled". A non-nil error means the binary exists
// but failed to start.
func New(ctx context.Context, workspace, defsDir string, timeout time.Duration) (*Diagnoser, error) {
	if _, err := exec.LookPath("lua-language-server"); err != nil {
		return nil, nil
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cfg := lsp.ServerConfig{
		Command: "lua-language-server",
		RootDir: workspace,
		// NOTE: the keys here are NOT wrapped in a top-level "Lua" table. The
		// lsp client answers LuaLS's workspace/configuration request (which asks
		// for the "Lua" section) by returning this whole map verbatim, so it
		// must already be the *contents* of the Lua namespace. Wrapping it in
		// "Lua" double-nests it and the library never loads (sdf reads as an
		// undefined global). Verified empirically against lua-language-server.
		Settings: map[string]any{
			"workspace": map[string]any{
				"library": []string{defsDir},
			},
			// Scripts use the global `sdf` form; the meta stub declares it but
			// LuaLS still flags it as undefined unless whitelisted.
			"diagnostics": map[string]any{
				"globals": []string{"sdf"},
			},
		},
	}
	cl, err := lsp.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Diagnoser{client: cl, timeout: timeout, opened: map[string]bool{}}, nil
}

// Check syncs content for the workspace-relative path and returns formatted
// diagnostics ("" when clean or when the Diagnoser is nil/disabled).
func (d *Diagnoser) Check(ctx context.Context, relpath, content string) string {
	if d == nil || d.client == nil {
		return ""
	}
	uri := d.client.URI(relpath)

	d.mu.Lock()
	first := !d.opened[uri]
	d.opened[uri] = true
	d.mu.Unlock()

	var err error
	if first {
		err = d.client.Open(ctx, uri, "lua", content)
	} else {
		_, err = d.client.Update(ctx, uri, content)
	}
	if err != nil {
		return ""
	}
	diags, _ := d.client.WaitForDiagnostics(ctx, uri, d.timeout)
	return formatDiags(relpath, diags)
}

// Warmup opens a throwaway document so lua-language-server finishes its slow
// initial meta preload before the first real Check. Safe to call in a
// goroutine; a nil Diagnoser is a no-op.
func (d *Diagnoser) Warmup(ctx context.Context) {
	if d == nil || d.client == nil {
		return
	}
	uri := d.client.URI("__cadpad_warmup__.lua")
	if err := d.client.Open(ctx, uri, "lua", "local _ = 1\n"); err != nil {
		return
	}
	_, _ = d.client.WaitForDiagnostics(ctx, uri, d.timeout)
	_ = d.client.Close(ctx, uri)
}

// Close shuts the language server down. A nil Diagnoser is a no-op.
func (d *Diagnoser) Close(ctx context.Context) {
	if d == nil || d.client == nil {
		return
	}
	_ = d.client.Shutdown(ctx)
}
