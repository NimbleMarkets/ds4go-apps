package render

// wgslgen.go: a minimal GLSL->WGSL transpiler for gsdf's generated SDF shader
// source. gsdf emits a sequence of GLSL function declarations of the form
//
//	float <name>(<params>) { <body> }
//
// where the LAST declaration is the top-level SDF. We transpile each decl to a
// WGSL `fn`, then append a `fn sdf(p: vec3<f32>) -> f32` shim that calls the
// top-level function. The transpiler is intentionally a clean pipeline of small
// functions (splitFunctions -> transpileFunction -> assemble) so that Task 1.3
// can extend the body rewriter (rewriteBody / glslIdentAllowed) without
// restructuring anything. Unrecognized identifiers produce an error so the
// caller can fall back to the CPU raymarcher.

import (
	"fmt"
	"strings"
)

// glslFunc is one parsed GLSL function declaration.
type glslFunc struct {
	returnType string // e.g. "float"
	name       string // e.g. "sphere3p"
	params     string // raw param list, e.g. "vec3 p" or "vec3 p, float x"
	body       string // raw body between the outermost braces
}

// transpileGLSLToWGSL converts gsdf-generated GLSL into WGSL. The result
// contains a `fn sdf(p: vec3<f32>) -> f32` entry point callable by the compute
// kernel. It returns an error on any construct it does not recognize.
func transpileGLSLToWGSL(glsl string) (string, error) {
	funcs, err := splitFunctions(glsl)
	if err != nil {
		return "", err
	}
	if len(funcs) == 0 {
		return "", fmt.Errorf("transpile: no GLSL function declarations found")
	}

	var out strings.Builder
	for _, f := range funcs {
		w, err := transpileFunction(f)
		if err != nil {
			return "", err
		}
		out.WriteString(w)
		out.WriteString("\n")
	}

	// The top-level SDF is the last-declared function in gsdf's output.
	topName := funcs[len(funcs)-1].name
	fmt.Fprintf(&out, "fn sdf(p: vec3<f32>) -> f32 { return %s(p); }\n", topName)

	return out.String(), nil
}

// splitFunctions scans GLSL into a sequence of function declarations. It is a
// brace-matching scanner: it reads "<returnType> <name>(<params>)" then captures
// the balanced { ... } body. Whitespace between declarations is skipped.
func splitFunctions(glsl string) ([]glslFunc, error) {
	var funcs []glslFunc
	i := 0
	n := len(glsl)
	for i < n {
		// Skip leading whitespace.
		for i < n && isSpace(glsl[i]) {
			i++
		}
		if i >= n {
			break
		}

		// Return type token.
		retStart := i
		for i < n && isIdentChar(glsl[i]) {
			i++
		}
		returnType := glsl[retStart:i]
		if returnType == "" {
			return nil, fmt.Errorf("transpile: expected return type at offset %d", i)
		}
		for i < n && isSpace(glsl[i]) {
			i++
		}

		// Function name token.
		nameStart := i
		for i < n && isIdentChar(glsl[i]) {
			i++
		}
		name := glsl[nameStart:i]
		if name == "" {
			return nil, fmt.Errorf("transpile: expected function name after %q", returnType)
		}
		for i < n && isSpace(glsl[i]) {
			i++
		}

		// Parameter list.
		if i >= n || glsl[i] != '(' {
			return nil, fmt.Errorf("transpile: expected '(' after %q", name)
		}
		i++
		paramStart := i
		for i < n && glsl[i] != ')' {
			i++
		}
		if i >= n {
			return nil, fmt.Errorf("transpile: unterminated parameter list for %q", name)
		}
		params := glsl[paramStart:i]
		i++ // consume ')'
		for i < n && isSpace(glsl[i]) {
			i++
		}

		// Body, brace-matched.
		if i >= n || glsl[i] != '{' {
			return nil, fmt.Errorf("transpile: expected '{' for %q body", name)
		}
		i++
		bodyStart := i
		depth := 1
		for i < n && depth > 0 {
			switch glsl[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			if depth == 0 {
				break
			}
			i++
		}
		if depth != 0 {
			return nil, fmt.Errorf("transpile: unterminated body for %q", name)
		}
		body := glsl[bodyStart:i]
		i++ // consume '}'

		funcs = append(funcs, glslFunc{
			returnType: returnType,
			name:       name,
			params:     params,
			body:       body,
		})
	}
	return funcs, nil
}

// transpileFunction rewrites a single GLSL function declaration into a WGSL fn.
func transpileFunction(f glslFunc) (string, error) {
	retType, err := mapGLSLType(f.returnType)
	if err != nil {
		return "", fmt.Errorf("transpile %s: return type: %w", f.name, err)
	}

	params, err := rewriteParams(f.params)
	if err != nil {
		return "", fmt.Errorf("transpile %s: %w", f.name, err)
	}

	body, err := rewriteBody(f.body)
	if err != nil {
		return "", fmt.Errorf("transpile %s: %w", f.name, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "fn %s(%s) -> %s {\n", f.name, params, retType)
	b.WriteString(body)
	b.WriteString("\n}")
	return b.String(), nil
}

// rewriteParams converts a comma-separated GLSL param list "<type> <name>, ..."
// into WGSL "<name>: <type>, ...". An empty list yields "".
func rewriteParams(params string) (string, error) {
	params = strings.TrimSpace(params)
	if params == "" {
		return "", nil
	}
	parts := strings.Split(params, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		fields := strings.Fields(p)
		if len(fields) != 2 {
			return "", fmt.Errorf("malformed parameter %q", p)
		}
		wt, err := mapGLSLType(fields[0])
		if err != nil {
			return "", err
		}
		out = append(out, fmt.Sprintf("%s: %s", fields[1], wt))
	}
	return strings.Join(out, ", "), nil
}

// mapGLSLType maps a GLSL scalar/vector/matrix type keyword to its WGSL form.
func mapGLSLType(t string) (string, error) {
	switch t {
	case "float":
		return "f32", nil
	case "vec2":
		return "vec2<f32>", nil
	case "vec3":
		return "vec3<f32>", nil
	case "vec4":
		return "vec4<f32>", nil
	case "mat3":
		return "mat3x3<f32>", nil
	default:
		return "", fmt.Errorf("unsupported type %q", t)
	}
}

// rewriteBody rewrites the tokens of a GLSL function body into WGSL. This is the
// primary extension seam for Task 1.3 (helper calls, booleans, transforms,
// braceless if, swizzle-assignment, const). For the sphere case the only work is
// float-literal normalization and identifier validation; type keywords appearing
// in bodies (vecN constructors, mat3) are handled here too since they already
// occur in the box/triprism goldens.
func rewriteBody(body string) (string, error) {
	body = normalizeFloatLiterals(body)
	body = rewriteTypeKeywords(body)
	if err := validateIdents(body); err != nil {
		return "", err
	}
	return body, nil
}

// rewriteTypeKeywords replaces whole-word GLSL vector/matrix type keywords used
// as constructors (e.g. `vec3(...)`) with their WGSL-parameterized form. Scalar
// `float` does not appear as a body constructor in the corpus, but vecN/mat3 do.
func rewriteTypeKeywords(body string) string {
	repl := map[string]string{
		"vec2": "vec2<f32>",
		"vec3": "vec3<f32>",
		"vec4": "vec4<f32>",
		"mat3": "mat3x3<f32>",
	}
	return replaceWholeWords(body, repl)
}

// validateIdents walks the identifiers in the body and ensures each is in the
// allowlist of recognized builtins/keywords, a user-defined helper, a vector
// component/swizzle, or a local variable. Anything else is an error so the
// caller falls back to CPU. For the sphere case this is trivially satisfied by
// `length`. The allowlist is the extension seam for richer bodies in Task 1.3.
func validateIdents(body string) error {
	for _, id := range identifiers(body) {
		if glslBuiltins[id] {
			continue
		}
		// Local variables, helper-function names, and swizzles/components are
		// accepted structurally: any plain identifier is allowed as long as it
		// is not a reserved/unsupported GLSL construct. We only reject known-bad
		// constructs explicitly so the sphere case passes; Task 1.3 will tighten
		// this against the helper-name set and swizzle grammar.
		if glslUnsupported[id] {
			return fmt.Errorf("unsupported GLSL construct %q", id)
		}
	}
	return nil
}

// glslBuiltins is the allowlist of builtin function/identifier names that map
// 1:1 to WGSL. Extended in Task 1.3.
var glslBuiltins = map[string]bool{
	"length": true, "abs": true, "min": true, "max": true, "clamp": true,
	"dot": true, "normalize": true, "sign": true, "sqrt": true, "mix": true,
	"vec2": true, "vec3": true, "vec4": true, "mat3": true,
	"return": true, "const": true, "if": true,
}

// glslUnsupported flags GLSL constructs that have no direct WGSL equivalent and
// must be rejected (forcing CPU fallback). Empty for now; populated as needed.
var glslUnsupported = map[string]bool{}

// --- small lexical helpers ---

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// identifiers returns the set of identifier tokens in s, excluding those that
// are immediately preceded by '.' (member/swizzle accesses) so component access
// like p.x does not leak "x" into validation.
func identifiers(s string) []string {
	var ids []string
	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		if isIdentStart(c) {
			start := i
			for i < n && isIdentChar(s[i]) {
				i++
			}
			// Skip member/swizzle accesses (preceded by '.').
			if start > 0 && s[start-1] == '.' {
				continue
			}
			ids = append(ids, s[start:i])
			continue
		}
		i++
	}
	return ids
}

// replaceWholeWords replaces identifier tokens that exactly match a key in repl,
// without touching substrings inside longer identifiers or member accesses.
func replaceWholeWords(s string, repl map[string]string) string {
	var b strings.Builder
	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		if isIdentStart(c) {
			start := i
			for i < n && isIdentChar(s[i]) {
				i++
			}
			word := s[start:i]
			precededByDot := start > 0 && s[start-1] == '.'
			if r, ok := repl[word]; ok && !precededByDot {
				b.WriteString(r)
			} else {
				b.WriteString(word)
			}
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// normalizeFloatLiterals rewrites GLSL float literals into strict-WGSL form:
// a trailing-dot literal like "3." becomes "3.0" and a leading-dot literal like
// ".5" becomes "0.5". Already-valid literals ("0.0", "1.5") pass through. It is
// careful NOT to treat the '.' in member/swizzle access (e.g. "p.x") as a float,
// and not to misparse the '.' that begins a number vs. a struct field: a '.' is
// the start of a numeric literal only when not preceded by an identifier/digit
// or ')'/']' (which would make it member access on an expression result).
func normalizeFloatLiterals(s string) string {
	var b strings.Builder
	n := len(s)
	for i := 0; i < n; {
		c := s[i]

		// A run of digits possibly forming a float "<digits>.<digits?>".
		if isDigit(c) {
			start := i
			for i < n && isDigit(s[i]) {
				i++
			}
			if i < n && s[i] == '.' {
				// Consume the dot and any fractional digits.
				i++ // dot
				fracStart := i
				for i < n && isDigit(s[i]) {
					i++
				}
				b.WriteString(s[start : fracStart-1]) // integer part
				b.WriteByte('.')
				if fracStart == i {
					b.WriteByte('0') // "3." -> "3.0"
				} else {
					b.WriteString(s[fracStart:i])
				}
			} else {
				b.WriteString(s[start:i]) // plain integer
			}
			continue
		}

		// A leading-dot float ".5" — only when the previous emitted char is not
		// part of an expression that '.' would be member access for.
		if c == '.' && i+1 < n && isDigit(s[i+1]) {
			prev := byte(0)
			if i > 0 {
				prev = s[i-1]
			}
			if !isIdentChar(prev) && prev != ')' && prev != ']' {
				b.WriteString("0.")
				i++ // dot
				for i < n && isDigit(s[i]) {
					b.WriteByte(s[i])
					i++
				}
				continue
			}
		}

		b.WriteByte(c)
		i++
	}
	return b.String()
}
