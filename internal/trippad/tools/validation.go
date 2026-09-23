package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/params"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/wgslls"
)

type shaderArgs struct {
	Mode      string          `json:"mode"`
	Name      string          `json:"name"`
	Body      *string         `json:"shade_body"`
	Params    *[]params.Param `json:"params"`
	Line      int             `json:"line"`
	Character int             `json:"character"`
}

func candidate(snap Snapshot, raw json.RawMessage, required bool) (shader.Source, map[string]float32, shaderArgs, error) {
	var a shaderArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return shader.Source{}, nil, a, err
	}
	if required && a.Body == nil {
		return shader.Source{}, nil, a, fmt.Errorf("shade_body is required")
	}
	if a.Mode != "" && a.Mode != "new" && a.Mode != "edit" {
		return shader.Source{}, nil, a, fmt.Errorf("mode must be new or edit")
	}
	if a.Body != nil && a.Mode == "" {
		return shader.Source{}, nil, a, fmt.Errorf("mode is required with shade_body: use new for a new design, edit for changes to the live shader")
	}
	src, values := snap.Source, snap.Named
	if a.Mode == "new" {
		if a.Body == nil || strings.TrimSpace(a.Name) == "" || a.Params == nil {
			return shader.Source{}, nil, a, fmt.Errorf("new designs require shade_body, a fresh name, and an explicit params array (use [] for no controls)")
		}
		if strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(snap.Source.Name)) {
			return shader.Source{}, nil, a, fmt.Errorf("new design name must differ from the live shader %q; choose a descriptive new name, or use mode edit to revise the current design", snap.Source.Name)
		}
		src, values = shader.Source{}, nil
	}
	if a.Body != nil {
		src.ShadeBody = *a.Body
	}
	if strings.TrimSpace(a.Name) != "" {
		src.Name = strings.TrimSpace(a.Name)
	}
	if a.Params != nil {
		src.Params = *a.Params
		values = nil
	}
	return src, values, a, nil
}

func limitText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "\n[diagnostic truncated]"
}

var wgslPosition = regexp.MustCompile(`(?:line (\d+), column (\d+)|(\d+):(\d+):)`)
var nestedFunction = regexp.MustCompile(`(?m)^\s*fn\s+`)

func shaderHints(body string) []string {
	var hints []string
	if nestedFunction.MatchString(body) {
		hints = append(hints, "shade_body is already inside fn shade: nested fn declarations are invalid. Inline the calculation or use the supplied helpers.")
	}
	if strings.Contains(body, "atan2(") {
		hints = append(hints, "Visual check: atan2 wraps at -pi/+pi. Raw angle used as a color or displacement can create a seam; use a periodic angular expression when continuity is intended. This is an advisory, not a compiler error.")
	}
	return hints
}

func compileDiagnostic(src shader.Source, err error) wgslls.Diagnostic {
	d := wgslls.Diagnostic{Location: "compiler", Severity: "error", Message: limitText(err.Error(), 4000)}
	doc, e := shader.BuildDocument(src)
	if e != nil {
		return d
	}
	// Backend MSL locations refer to translated code, not our WGSL template.
	if strings.Contains(err.Error(), "MSL") || strings.Contains(err.Error(), "program_source:") {
		d.Location = "backend"
		return d
	}
	if p := wgslPosition.FindStringSubmatch(err.Error()); p != nil {
		ln, col := p[1], p[2]
		if ln == "" {
			ln, col = p[3], p[4]
		}
		line, _ := strconv.Atoi(ln)
		d.Column, _ = strconv.Atoi(col)
		d.Location, d.Line = wgslls.Location(doc, line)
	}
	return d
}

func validationHandler(server *wgslls.Service) Handler {
	return func(ctx context.Context, s *State, raw json.RawMessage) (ds4.ToolResult, error) {
		snap := s.Snapshot()
		src, _, _, err := candidate(snap, raw, false)
		if err != nil {
			return ds4.ToolResult{}, err
		}
		doc, err := shader.BuildDocument(src)
		if err != nil {
			return ds4.ToolResult{}, err
		}
		report := struct {
			Name            string              `json:"name"`
			BodyMatchesLive bool                `json:"body_matches_live"`
			ParameterUsage  parameterUsage      `json:"parameter_usage"`
			CompileOK       bool                `json:"compile_ok"`
			LSPStatus       string              `json:"lsp_status"`
			Diagnostics     []wgslls.Diagnostic `json:"diagnostics"`
			Hints           []string            `json:"hints,omitempty"`
		}{Name: src.Name, BodyMatchesLive: src.ShadeBody == snap.Source.ShadeBody, ParameterUsage: inspectParams(src), Diagnostics: []wgslls.Diagnostic{}, Hints: shaderHints(src.ShadeBody)}
		report.Hints = append(report.Hints, report.ParameterUsage.hints()...)
		if report.BodyMatchesLive {
			report.Hints = append(report.Hints, "shade_body is identical to the live shader; this is not a body rewrite.")
		}
		if err := ctx.Err(); err != nil {
			return ds4.ToolResult{}, err
		}
		if err := s.renderer.Compile(src); err != nil {
			report.Diagnostics = append(report.Diagnostics, compileDiagnostic(src, err))
		} else {
			report.CompileOK = true
		}
		if err := ctx.Err(); err != nil {
			return ds4.ToolResult{}, err
		}
		diags, err := server.Check(ctx, doc)
		if ctx.Err() != nil {
			return ds4.ToolResult{}, ctx.Err()
		}
		report.LSPStatus = "checked"
		if err != nil {
			report.LSPStatus = "unavailable: " + limitText(err.Error(), 400)
		}
		if len(diags) > 20 {
			diags = diags[:20]
			report.Hints = append(report.Hints, "Only the first 20 language-server diagnostics are shown.")
		}
		for i := range diags {
			diags[i].Message = limitText(diags[i].Message, 1200)
		}
		report.Diagnostics = append(report.Diagnostics, diags...)
		return jsonResult(report)
	}
}

func completionHandler(server *wgslls.Service) Handler {
	return func(ctx context.Context, s *State, raw json.RawMessage) (ds4.ToolResult, error) {
		src, _, a, err := candidate(s.Snapshot(), raw, false)
		if err != nil {
			return ds4.ToolResult{}, err
		}
		out, err := server.Complete(ctx, src, a.Line, a.Character)
		if ctx.Err() != nil {
			return ds4.ToolResult{}, ctx.Err()
		}
		if err != nil {
			return ds4.ToolResult{}, err
		}
		if out == "" {
			out = "No language-server information at this position."
		}
		return textResult(limitText(out, 6000)), nil
	}
}
