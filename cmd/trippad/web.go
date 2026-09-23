package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/library"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
	tripweb "github.com/NimbleMarkets/ds4go-apps/internal/trippad/web"
)

// Browser-only mode avoids model, memory and native GPU initialization.
func serveBrowser(addr, preset, galleryDir string, fps int) error {
	src := shader.Starters()[0]
	var named map[string]float32
	if preset != "" {
		p, err := params.Load(preset)
		if err != nil {
			return err
		}
		src = shader.Source{Name: p.Name, ShadeBody: p.Shader, Params: p.Params}
		named = p.Values
	}
	if _, err := shader.BuildWGSL(src); err != nil {
		return err
	}
	values, err := params.New(src.Params)
	if err != nil {
		return err
	}
	for k, v := range named {
		if _, err := values.Set(k, v); err != nil {
			return err
		}
	}
	snap := tools.Snapshot{Source: src, Named: values.Named(), Width: 640, Height: 480}
	server, err := tripweb.Start(addr, func() tools.Snapshot { return snap }, fps, false, &library.Store{Dir: galleryDir})
	if err != nil {
		return err
	}
	defer server.Close()
	fmt.Printf("Trippad WebGPU: %s\nCtrl+C to stop.\n", server.URL)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
		return nil
	case err := <-server.Done():
		return err
	}
}
