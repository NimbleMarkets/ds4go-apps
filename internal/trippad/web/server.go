// Package web serves a local WebGPU comparison using trippad's exact compute WGSL.
package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/library"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
)

//go:embed assets/*
var assets embed.FS

type Snapshot struct {
	tools.Snapshot
	WGSL      string `json:"wgsl"`
	TargetFPS int    `json:"target_fps"`
	Native    bool   `json:"native"`
}

func Handler(snapshot func() tools.Snapshot, fps int, native bool, store *library.Store) http.Handler {
	files, _ := fs.Sub(assets, "assets")
	mux := http.NewServeMux()
	mountGallery(mux, store, snapshot, fps)
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		s := snapshot()
		code, err := shader.BuildWGSL(s.Source)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Snapshot{Snapshot: s, WGSL: code, TargetFPS: fps, Native: native})
	})
	mux.Handle("GET /", http.FileServer(http.FS(files)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only embedded assets and enumerated gallery presets, with no mutations.
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "same-origin requests only", http.StatusForbidden)
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if !loopback(host) {
			http.Error(w, "loopback host required", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type Server struct {
	URL    string
	server *http.Server
	done   chan error
}

// Start accepts loopback only; localhost HTTP is a browser secure context.
func Start(addr string, snapshot func() tools.Snapshot, fps int, native bool, store *library.Store) (*Server, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !loopback(host) {
		return nil, fmt.Errorf("--web requires a loopback address, e.g. 127.0.0.1:8080")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("start browser demo: %w", err)
	}
	server := &http.Server{Handler: Handler(snapshot, fps, native, store), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	s := &Server{URL: "http://" + listener.Addr().String() + "/", server: server, done: make(chan error, 1)}
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		s.done <- err
	}()
	return s, nil
}
func (s *Server) Close() error       { return s.server.Close() }
func (s *Server) Done() <-chan error { return s.done }
