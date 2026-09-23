package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/tools"
)

func TestStateUsesExactNativeShaderAndLiveValues(t *testing.T) {
	src := shader.Starters()[0]
	live := tools.Snapshot{Source: src, Named: map[string]float32{src.Params[0].Name: 1.25}, Time: 3, Width: 912, Height: 600, Performance: tools.Performance{RenderMS: 7, EncodeMS: 4, UploadBytes: 1234, SampledAt: 123}}
	handler := Handler(func() tools.Snapshot { return live }, 30, true, nil)
	for _, value := range []float32{1.25, 2} {
		live.Named[src.Params[0].Name] = value
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/state", nil))
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Code, rec.Body.String())
		}
		var got Snapshot
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want, err := shader.BuildWGSL(src)
		if err != nil {
			t.Fatal(err)
		}
		if got.WGSL != want || got.Named[src.Params[0].Name] != value || got.Width != 912 || got.Time != 3 || got.TargetFPS != 30 || !got.Native || got.Performance != live.Performance {
			t.Fatalf("wrong snapshot: %+v", got)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("live state cached")
		}
	}
}

func TestEmbeddedAssetsAndReadOnlyAPI(t *testing.T) {
	handler := Handler(func() tools.Snapshot { return tools.Snapshot{Source: shader.Starters()[0]} }, 60, false, nil)
	for _, tc := range []struct {
		method, path string
		status       int
		contains     string
	}{
		{"GET", "/", 200, "Load live state"},
		{"GET", "/app.js", 200, "createComputePipelineAsync"},
		{"GET", "/style.css", 200, "color-scheme"},
		{"GET", "/missing.trip.json", 404, ""},
		{"POST", "/api/state", 405, ""},
		{"GET", "/server.go", 404, ""},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tc.method, "http://localhost"+tc.path, nil))
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.contains) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	for _, tc := range []struct{ host, site string }{{"attacker.example", ""}, {"localhost", "cross-site"}, {"127.0.0.1", "same-site"}} {
		req := httptest.NewRequest("GET", "http://"+tc.host+"/api/state", nil)
		req.Header.Set("Sec-Fetch-Site", tc.site)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestRejectNonLoopbackBind(t *testing.T) {
	for _, addr := range []string{":8080", "0.0.0.0:8080", "[::]:8080", "example.com:8080", "invalid"} {
		server, err := Start(addr, nil, 60, false, nil)
		if err == nil {
			server.Close()
			t.Fatalf("accepted %q", addr)
		}
	}
}
