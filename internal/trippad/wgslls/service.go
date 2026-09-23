// Package wgslls adapts ds4go's LSP client to trippad's generated WGSL modules.
package wgslls

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go/lsp"
)

// Find prefers the tested adjacent/project installation, then PATH.
func Find(command string) string {
	if command == "off" {
		return ""
	}
	if command != "auto" && command != "" {
		return command
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "wgsl-analyzer")
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return p
		}
	}
	if p, err := filepath.Abs("bin/wgsl-analyzer"); err == nil {
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return p
		}
	}
	if p, err := exec.LookPath("wgsl-analyzer"); err == nil {
		return p
	}
	return ""
}

type Service struct {
	Command     string
	mu          sync.Mutex
	client      *lsp.Client
	dir         string
	text        string
	uri         string
	sequence    uint64
	updates     chan []lsp.Diagnostic
	checked     bool
	diagnostics []lsp.Diagnostic
	verified    bool
	verifyErr   error
}

func New(command string) *Service { return &Service{Command: Find(command)} }

// Start lazily: initialization and analysis run on the generation worker, not
// the UI thread. The server persists until Close, independent of model changes.
func (s *Service) start(ctx context.Context) error {
	if s.client != nil {
		return nil
	}
	if s.Command == "" {
		return fmt.Errorf("wgsl-analyzer unavailable; install it on PATH or beside the trippad binary")
	}
	dir, err := os.MkdirTemp("", "trippad-wgsl-")
	if err != nil {
		return err
	}
	settings := map[string]any{
		"customImports": map[string]string{}, "shaderDefs": []string{}, "trace": map[string]bool{"extension": false, "server": false},
		"inlayHints":  map[string]any{"enabled": false, "typeHints": false, "parameterHints": false, "structLayoutHints": false, "typeVerbosity": "compact"},
		"diagnostics": map[string]any{"typeErrors": true, "nagaParsingErrors": true, "nagaValidationErrors": true, "nagaVersion": "0.22"},
	}
	c, err := lsp.New(ctx, lsp.ServerConfig{Command: s.Command, RootDir: dir, Timeout: 5 * time.Second, ShutdownTimeout: time.Second,
		// A silent server must time out rather than falsely confirm clean code.
		FirstWait: time.Hour, SettleWait: 150 * time.Millisecond,
		InitOptions: settings, Settings: settings,
	})
	if err != nil {
		os.RemoveAll(dir)
		return err
	}
	s.dir, s.client = dir, c
	return nil
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.client.Shutdown(ctx)
		s.client = nil
		s.uri = ""
		s.checked, s.verified = false, false
		s.verifyErr = nil
		s.diagnostics = nil
		s.text = ""
	}
	if s.dir != "" {
		os.RemoveAll(s.dir)
		s.dir = ""
	}
}

// Use a distinct URI per candidate: the pinned server publishes unversioned
// diagnostics. This prevents delayed results for an earlier edit being mistaken
// for the current candidate. Keep only one document open in the persistent server.
func (s *Service) document(ctx context.Context, doc shader.Document) (string, error) {
	if err := s.start(ctx); err != nil {
		return "", err
	}
	if s.uri != "" && s.text == doc.Code {
		return s.uri, nil
	}
	if s.uri != "" {
		if err := s.client.Close(ctx, s.uri); err != nil {
			return "", err
		}
		s.uri = ""
	}
	s.sequence++
	name := fmt.Sprintf("candidate-%d.wgsl", s.sequence)
	uri := s.client.URI(name)
	updates := make(chan []lsp.Diagnostic, 32)
	s.client.OnDiagnostics(func(got string, _ int32, ds []lsp.Diagnostic) {
		if !strings.HasSuffix(got, "/"+name) {
			return
		}
		select {
		case updates <- ds:
		default:
		}
	})
	if err := s.client.Open(ctx, uri, "wgsl", doc.Code); err != nil {
		return "", err
	}
	s.uri, s.text, s.updates = uri, doc.Code, updates
	s.checked, s.diagnostics = false, nil
	return uri, nil
}

// The callback is installed before didOpen, so an early empty publish still
// counts as a completed analysis. Silence is never interpreted as clean code.
func (s *Service) checkDocument(ctx context.Context, doc shader.Document) ([]lsp.Diagnostic, error) {
	if _, err := s.document(ctx, doc); err != nil {
		return nil, err
	}
	if s.checked {
		return s.diagnostics, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var settle <-chan time.Time
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("WGSL diagnostics did not complete: %w", ctx.Err())
		case ds := <-s.updates:
			s.diagnostics = ds
			timer.Reset(150 * time.Millisecond)
			settle = timer.C
		case <-settle:
			s.checked = true
			return s.diagnostics, nil
		}
	}
}

func (s *Service) verify(ctx context.Context) error {
	if s.verified {
		return s.verifyErr
	}
	doc, _ := shader.BuildDocument(shader.Source{ShadeBody: "let bad: f32 = vec3<f32>(1.0);\nreturn vec3<f32>(bad);"})
	ds, err := s.checkDocument(ctx, doc)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		err = fmt.Errorf("server reported a deliberately invalid shader as clean; run scripts/install-trippad-wgsl-lsp.sh for the tested NimbleMarkets fork")
		for _, d := range ds {
			if d.Severity.String() == "error" && d.Line >= doc.BodyStart && d.Line <= doc.BodyEnd {
				err = nil
				break
			}
		}
	}
	s.verified, s.verifyErr = true, err
	return err
}

type Diagnostic struct {
	Location string `json:"location"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func Location(doc shader.Document, line int) (string, int) {
	if line >= doc.BodyStart && line <= doc.BodyEnd {
		return "shade_body", line - doc.BodyStart + 1
	}
	return "full_wgsl", line
}

func (s *Service) Check(ctx context.Context, doc shader.Document) ([]Diagnostic, error) {
	if s == nil {
		return nil, fmt.Errorf("WGSL language server is disabled")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.verify(ctx); err != nil {
		return nil, err
	}
	ds, err := s.checkDocument(ctx, doc)
	out := make([]Diagnostic, 0, len(ds))
	for _, d := range ds {
		loc, line := Location(doc, d.Line)
		out = append(out, Diagnostic{Location: loc, Line: line, Column: d.Col, Severity: d.Severity.String(), Message: d.Message})
	}
	return out, err
}

// Complete uses 1-based UTF-8 byte columns, as required by the pinned upstream LSP.
func (s *Service) Complete(ctx context.Context, src shader.Source, line, col int) (string, error) {
	if s == nil {
		return "", fmt.Errorf("WGSL language server is disabled")
	}
	doc, err := shader.BuildDocument(src)
	if err != nil {
		return "", err
	}
	lines := strings.Split(src.ShadeBody, "\n")
	if line < 1 || line > len(lines) || col < 1 || col > len(lines[line-1])+1 {
		return "", fmt.Errorf("position must be inside shade_body (1-based line and UTF-8 byte column)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.verify(ctx); err != nil {
		return "", err
	}
	uri, err := s.document(ctx, doc)
	if err != nil {
		return "", err
	}
	labels, more, err := s.client.Completion(ctx, uri, line+doc.BodyStart-1, col, 25)
	out := strings.Join(labels, ", ")
	if more > 0 {
		out += fmt.Sprintf("\n(%d more suggestions omitted)", more)
	}
	return out, err
}
