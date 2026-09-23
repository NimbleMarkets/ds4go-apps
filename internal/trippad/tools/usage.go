package tools

import (
	"strings"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/shader"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// This is a lexical reference check, not proof that a parameter affects pixels.
// Tokenizing WGSL excludes comments (including nested block comments) and avoids
// counting a reference to p_scale_extra as a reference to p_scale.
type parameterUsage struct {
	Checked    bool     `json:"checked"`
	Referenced []string `json:"referenced"`
	Unused     []string `json:"unused"`
}

func inspectParams(src shader.Source) parameterUsage {
	out := parameterUsage{Referenced: []string{}, Unused: []string{}}
	lexer := lexers.Get("wgsl")
	if lexer == nil {
		return out
	}
	it, err := lexer.Tokenise(nil, src.ShadeBody)
	if err != nil {
		return out
	}
	calls := make(map[string]bool)
	previous := ""
	for token := it(); token != chroma.EOF; token = it() {
		if token.Type.InCategory(chroma.Comment) || strings.TrimSpace(token.Value) == "" {
			continue
		}
		if previous != "" && strings.HasPrefix(token.Value, "(") {
			calls[previous] = true
		}
		previous = ""
		if token.Type.InCategory(chroma.Name) && strings.HasPrefix(token.Value, "p_") {
			previous = strings.TrimPrefix(token.Value, "p_")
		}
	}
	out.Checked = true
	for _, p := range src.Params {
		if calls[p.Name] {
			out.Referenced = append(out.Referenced, p.Name)
		} else {
			out.Unused = append(out.Unused, p.Name)
		}
	}
	return out
}

func (u parameterUsage) hints() []string {
	if !u.Checked {
		return []string{"Parameter reference check unavailable; check that each intended control is used in shade_body."}
	}
	if len(u.Unused) == 0 {
		return nil
	}
	return []string{"Unused controls: " + strings.Join(u.Unused, ", ") + ". Their p_NAME() accessors are not called in shade_body. Wire them into the shader or submit a params array without them; inherited controls are not removed automatically."}
}
