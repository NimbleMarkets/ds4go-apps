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
// can extend the body rewriter (rewriteBody / validateIdents) without
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

	// Collect the names of all functions declared in this program so that calls
	// between helpers (e.g. extrusion -> circle2p -> gsdfEqTri) are recognized
	// as known identifiers rather than rejected as unknown.
	declared := make(map[string]bool, len(funcs))
	for _, f := range funcs {
		declared[f.name] = true
	}

	var out strings.Builder
	for _, f := range funcs {
		w, err := transpileFunction(f, declared)
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
// declared is the set of function names declared in the whole program, used to
// validate inter-function calls.
func transpileFunction(f glslFunc, declared map[string]bool) (string, error) {
	retType, err := mapGLSLType(f.returnType)
	if err != nil {
		return "", fmt.Errorf("transpile %s: return type: %w", f.name, err)
	}

	// WGSL function parameters are IMMUTABLE: gsdf freely reassigns the position
	// parameter (`p = abs(p);`, `p = p.xzy;`, `p.x = ...;`), which is a compile
	// error on a WGSL param. For every parameter the body writes to, we rename
	// the incoming param to `<name>_in` and seed a mutable `var <name> = <name>_in;`
	// as the first body statement. Callers pass arguments positionally, so the
	// rename is invisible across the call boundary.
	mutated := assignedParams(f.body, paramNames(f.params))

	params, err := rewriteParams(f.params, mutated)
	if err != nil {
		return "", fmt.Errorf("transpile %s: %w", f.name, err)
	}

	// The set of identifiers legal in this body besides builtins and declared
	// functions: the function's own parameters plus any locals it declares.
	// Seeded params keep their original name as an in-body local; their `_in`
	// alias is a synthetic name that never appears in the body, so it needs no
	// scope entry.
	scope := paramNames(f.params)
	for name := range collectLocals(f.body) {
		scope[name] = true
	}

	body, err := rewriteBody(splatVectorBounds(f.body, f.params), declared, scope)
	if err != nil {
		return "", fmt.Errorf("transpile %s: %w", f.name, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "fn %s(%s) -> %s {\n", f.name, params, retType)
	// Seed mutable locals for every mutated parameter, in stable order.
	for _, name := range orderedParamNames(f.params) {
		if mutated[name] {
			fmt.Fprintf(&b, "var %s = %s_in;\n", name, name)
		}
	}
	b.WriteString(body)
	b.WriteString("\n}")
	return b.String(), nil
}

// rewriteParams converts a comma-separated GLSL param list "<type> <name>, ..."
// into WGSL "<name>: <type>, ...". Parameters whose name is in mutated are
// emitted as "<name>_in: <type>" so the body can seed a mutable local of the
// original name (see transpileFunction). An empty list yields "".
func rewriteParams(params string, mutated map[string]bool) (string, error) {
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
		name := fields[1]
		if mutated[name] {
			name += "_in"
		}
		out = append(out, fmt.Sprintf("%s: %s", name, wt))
	}
	return strings.Join(out, ", "), nil
}

// orderedParamNames returns the parameter names in declaration order. (paramNames
// returns a set, which has no stable iteration order; the seed prelude wants
// deterministic output.)
func orderedParamNames(params string) []string {
	params = strings.TrimSpace(params)
	if params == "" {
		return nil
	}
	var names []string
	for _, p := range strings.Split(params, ",") {
		fields := strings.Fields(strings.TrimSpace(p))
		if len(fields) == 2 {
			names = append(names, fields[1])
		}
	}
	return names
}

// assignedParams returns the subset of params that the body assigns to, whether
// as a whole (`p = ...`), a component (`p.x = ...`), or a swizzle (`p.xy -= ...`),
// including compound-assignment forms. A name is considered assigned when it (or
// a `.`-suffixed access on it) is immediately followed — modulo whitespace and an
// optional `.<swizzle>` — by an assignment operator (`=`, `+=`, `-=`, `*=`, `/=`)
// that is not the `==`/`>=`/`<=`/`!=` comparison form.
func assignedParams(body string, params map[string]bool) map[string]bool {
	assigned := make(map[string]bool)
	toks := tokenize(body)
	for i := 0; i < len(toks); i++ {
		name := toks[i]
		if !params[name] {
			continue
		}
		// Walk past an optional `.<member>` swizzle suffix: tokens ".", "<ident>".
		j := i + 1
		if j+1 < len(toks) && toks[j] == "." && isIdentStart(toks[j+1][0]) {
			j += 2
		}
		if j >= len(toks) {
			continue
		}
		// The next punctuation token must begin an assignment. tokenize emits
		// single-char punctuation, so a compound op like `-=` is ["-","="] and a
		// comparison like `>=` is [">","="]; a bare `=` is a plain assignment.
		switch toks[j] {
		case "=":
			// Plain assignment, but NOT the `==` comparison (tokenized "=","=").
			if j+1 >= len(toks) || toks[j+1] != "=" {
				assigned[name] = true
			}
		case "+", "-", "*", "/":
			if j+1 < len(toks) && toks[j+1] == "=" {
				assigned[name] = true
			}
		}
	}
	return assigned
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
	case "mat4":
		return "mat4x4<f32>", nil
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
func rewriteBody(body string, declared, scope map[string]bool) (string, error) {
	body = normalizeFloatLiterals(body)
	// Statement-level rewrites run on GLSL type keywords (`vec3 q`, `const float k`)
	// BEFORE rewriteTypeKeywords mangles them, so declaration detection sees the
	// raw `vec3`/`float`/`const` lead tokens. This ordering is load-bearing: the
	// paren-swizzle hoist inside rewriteStatements only correctly skips genuine
	// constructors (`vec4(...)`, ident-prefixed) while types are still GLSL-form;
	// after rewriteTypeKeywords they become `vec4<f32>(...)` whose `>(` would be
	// misread as a grouping-paren swizzle. Do not reorder these two calls.
	body = rewriteStatements(body)
	body = rewriteTypeKeywords(body)
	if err := validateIdents(body, declared, scope); err != nil {
		return "", err
	}
	return body, nil
}

// rewriteStatements rewrites GLSL statements into WGSL statements. It handles the
// two syntactic gaps between GLSL and WGSL that survive token-level rewriting:
//
//   - Local/const declarations. GLSL types its locals (`vec3 q = ...;`,
//     `const float k = ...;`); WGSL infers them. We emit `var <name> = <init>;`
//     for plain locals (some are reassigned, so `var` not `let`) and
//     `let <name> = <init>;` for `const` declarations (function-scope immutable).
//   - Braceless `if`. GLSL allows `if (c) stmt;`; WGSL requires a block. We wrap
//     the single controlled statement in `{ ... }`.
//
// The body is split into `;`-terminated statements. This is safe for the corpus:
// no statement contains an interior `;` (no for/while loops; the only control
// flow is a single-statement braceless `if`, whose condition has no `;`).
func rewriteStatements(body string) string {
	stmts := splitStatements(body)
	var out strings.Builder
	for _, s := range stmts {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		out.WriteString(rewriteStatement(trimmed))
		out.WriteString("\n")
	}
	return out.String()
}

// splitStatements splits a body on top-level ';'. Each returned element excludes
// its terminating ';'. A trailing fragment without a ';' (none in the corpus) is
// still returned.
func splitStatements(body string) []string {
	var stmts []string
	start := 0
	for i := 0; i < len(body); i++ {
		if body[i] == ';' {
			stmts = append(stmts, body[start:i])
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(body[start:]); rest != "" {
		stmts = append(stmts, body[start:])
	}
	return stmts
}

// rewriteStatement rewrites one trimmed, ';'-stripped GLSL statement into WGSL,
// re-appending the ';'. A braceless `if` is wrapped; declarations are converted;
// everything else (assignments, swizzle-assignments, `return`) passes through.
func rewriteStatement(s string) string {
	toks := tokenize(s)
	if len(toks) == 0 {
		return s + ";"
	}

	// Braceless if: `if ( <cond> ) <stmt>` -> `if (<cond>) { <stmt>; }`.
	if toks[0] == "if" {
		return rewriteBracelessIf(s)
	}

	// Hoist any `(<expr>).<swizzle>` applied to a parenthesized GROUPING
	// expression into a preceding temp. naga's MSL backend miscompiles a
	// multi-component swizzle taken directly off a parenthesized binary
	// expression — e.g. the transform op's `((invT) * vec4<f32>(p,0.0)).xyz` —
	// silently dropping the swizzle (a vec4->vec3 implicit conversion error, or
	// outright wrong codegen). Assigning the group to a `var` first and swizzling
	// that var is correct, so we lift it. This runs before declaration/return
	// handling; the prelude temps are emitted ahead of the (rewritten) statement.
	if prelude, rewritten := hoistParenSwizzle(s); len(prelude) > 0 {
		var b strings.Builder
		for _, p := range prelude {
			b.WriteString(p)
			b.WriteString("\n")
		}
		b.WriteString(rewriteStatement(rewritten))
		return b.String()
	}

	// `const <type> <name> = <init>` -> `let <name> = <init>`.
	if toks[0] == "const" && len(toks) >= 3 && glslDeclTypes[toks[1]] {
		rest := stripDeclHead(s, true)
		return "let " + rest + ";"
	}

	// `<type> <name> = <init>` or `<type> <name>` -> `var <name> [= <init>]`.
	// A bare type keyword used as a constructor (`vec3(...)`) is NOT a
	// declaration: the token after the type would be '(' not an identifier.
	if glslDeclTypes[toks[0]] && len(toks) >= 2 && isIdentStart(toks[1][0]) && !glslDeclTypes[toks[1]] {
		rest := stripDeclHead(s, false)
		return "var " + rest + ";"
	}

	// Multi-component swizzle-write assignment, e.g. `p.xy -= expr` or
	// `p.xy = expr`. naga miscompiles writes to a multi-component swizzle l-value
	// (the assignment silently produces wrong results — see the hexprism corpus
	// shape), so we lower it to explicit per-component writes through a temp:
	//   p.xy -= EXPR  ->  { let _s = (EXPR); p.x = p.x - (_s).x; p.y = p.y - (_s).y; }
	//   p.xy  = EXPR  ->  { let _s = (EXPR); p.x = (_s).x; p.y = (_s).y; }
	// Single-component writes (p.x = ...) are correct in naga and are left alone.
	if exp, ok := expandSwizzleWrite(s); ok {
		return exp
	}

	return s + ";"
}

// hoistParenSwizzle finds substrings of the form `(<group>).<swizzle>` where the
// `(` is a GROUPING paren (the preceding non-space char is not an identifier char
// and not ')'/']' — i.e. it is not a function call or an index/member result) and
// <swizzle> selects two or more components. naga's MSL backend miscompiles such an
// inline swizzle-of-a-parenthesized-expression, so each match is lifted into a
// fresh `var _hsN = (<group>);` prelude statement (the UN-swizzled group) and the
// inline occurrence is replaced by `_hsN.<swizzle>`. naga miscompiles the swizzle
// even when the parenthesized expression is itself a var initializer, so the temp
// must carry only the group, never the swizzle. It returns the prelude statements
// (in order) and the rewritten statement; an empty prelude means nothing changed.
//
// The `var` (not `let`) seed keeps the temp usable even if the surrounding
// statement is itself a declaration whose rewriting expects a mutable head; the
// temp is never reassigned, so `var` is harmless. Names are unique within the
// statement via a counter, and the "_hs" prefix is accepted by validateIdents.
func hoistParenSwizzle(s string) (prelude []string, rewritten string) {
	rewritten = s
	n := 0
	for {
		idx, group, swz, found := findParenSwizzle(rewritten)
		if !found {
			break
		}
		name := fmt.Sprintf("_hs%d", n)
		n++
		// Hoist the UN-swizzled group into the temp, then swizzle the temp inline.
		// naga miscompiles the swizzle even when the parenthesized expression is a
		// `var` initializer, so the temp must NOT carry the swizzle; only a plain
		// var-then-`.swz` form compiles correctly.
		prelude = append(prelude, fmt.Sprintf("var %s = (%s);", name, group))
		// Replace the matched `(group).swz` span with `temp.swz`.
		rewritten = rewritten[:idx.start] + name + "." + swz + rewritten[idx.lhsEnd:]
	}
	if n == 0 {
		return nil, s
	}
	return prelude, rewritten
}

// parenSwizzleSpan locates the byte span of a matched `(group).swizzle`.
type parenSwizzleSpan struct {
	start  int // index of the opening '('
	lhsEnd int // one past the last swizzle char
}

// findParenSwizzle scans s for the FIRST `(<group>).<swizzle>` whose '(' is a
// grouping paren and whose swizzle has 2+ components. It returns the span, the
// inner group text (without the parens), the swizzle string, and whether a match
// was found.
func findParenSwizzle(s string) (span parenSwizzleSpan, group, swz string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] != '(' {
			continue
		}
		// Determine whether this '(' is a grouping paren (not a call/index).
		prev := byte(0)
		for j := i - 1; j >= 0; j-- {
			if isSpace(s[j]) {
				continue
			}
			prev = s[j]
			break
		}
		if isIdentChar(prev) || prev == ')' || prev == ']' {
			continue // function call or member/index result — not a grouping paren
		}
		// Match the closing ')'.
		depth := 0
		close := -1
		for j := i; j < len(s); j++ {
			switch s[j] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					close = j
				}
			}
			if close >= 0 {
				break
			}
		}
		if close < 0 {
			return parenSwizzleSpan{}, "", "", false
		}
		// Require `.` + a 2+ component swizzle immediately after the ')'.
		if close+1 >= len(s) || s[close+1] != '.' {
			continue
		}
		k := close + 2
		swStart := k
		for k < len(s) {
			if _, ok := swizzleComponents[s[k]]; ok {
				k++
				continue
			}
			break
		}
		sw := s[swStart:k]
		if len(sw) < 2 {
			continue // single-component swizzle is fine for naga
		}
		// The swizzle run above consumed only {x,y,z,w} chars; if the immediately
		// following char is still an identifier char, this was a longer field name
		// (e.g. `.xyzw_foo`), not a pure swizzle — bail rather than mis-clip it.
		// gsdf emits no such names, so this is a conservative guard.
		if k < len(s) && isIdentChar(s[k]) {
			continue
		}
		group = strings.TrimSpace(s[i+1 : close])
		return parenSwizzleSpan{start: i, lhsEnd: k}, group, sw, true
	}
	return parenSwizzleSpan{}, "", "", false
}

// swizzleComponents maps a swizzle name to the underlying component selectors.
var swizzleComponents = map[byte]string{'x': "x", 'y': "y", 'z': "z", 'w': "w"}

// expandSwizzleWrite detects a statement of the form
// `<base>.<swizzle> [op]= <rhs>` where <swizzle> has two or more components and
// rewrites it into a braced block of per-component scalar assignments through a
// temporary. It returns the rewritten statement and true on a match; otherwise
// ("", false). <base> may itself be an identifier (the only form in the corpus);
// the rhs is captured verbatim. This sidesteps a naga codegen bug on
// multi-component swizzle l-values.
func expandSwizzleWrite(s string) (string, bool) {
	s = strings.TrimSpace(s)
	// Find the assignment operator at top level (paren depth 0): a '=' not part
	// of ==/<=/>=/!=, optionally preceded by one of + - * / (compound form).
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case '=':
			if depth != 0 {
				continue
			}
			// Skip comparison operators.
			if i+1 < len(s) && s[i+1] == '=' {
				return "", false
			}
			if i > 0 && (s[i-1] == '=' || s[i-1] == '<' || s[i-1] == '>' || s[i-1] == '!') {
				return "", false
			}
			op := byte(0)
			lhsEnd := i
			if i > 0 && (s[i-1] == '+' || s[i-1] == '-' || s[i-1] == '*' || s[i-1] == '/') {
				op = s[i-1]
				lhsEnd = i - 1
			}
			lhs := strings.TrimSpace(s[:lhsEnd])
			rhs := strings.TrimSpace(s[i+1:])
			return buildSwizzleExpansion(lhs, op, rhs)
		}
	}
	return "", false
}

// buildSwizzleExpansion turns lhs (must be `base.swizzle` with 2+ components),
// op ('+','-','*','/' or 0 for plain '='), and rhs into a braced block of
// per-component assignments. Returns ("", false) if lhs is not a multi-component
// swizzle.
func buildSwizzleExpansion(lhs string, op byte, rhs string) (string, bool) {
	dot := strings.LastIndexByte(lhs, '.')
	if dot <= 0 {
		return "", false
	}
	base := strings.TrimSpace(lhs[:dot])
	swz := lhs[dot+1:]
	if len(swz) < 2 {
		return "", false // single-component write is fine as-is
	}
	for i := 0; i < len(swz); i++ {
		if _, ok := swizzleComponents[swz[i]]; !ok {
			return "", false // not a pure swizzle (e.g. a method/field) — bail
		}
	}
	// base must be a plain identifier l-value for the per-component writes to be
	// valid (the corpus only ever swizzle-writes a local var).
	for i := 0; i < len(base); i++ {
		if !isIdentChar(base[i]) {
			return "", false
		}
	}
	var b strings.Builder
	b.WriteString("{ let _swz = (")
	b.WriteString(rhs)
	b.WriteString(");")
	for i := 0; i < len(swz); i++ {
		comp := string(swz[i])
		b.WriteString(" ")
		b.WriteString(base)
		b.WriteString(".")
		b.WriteString(comp)
		b.WriteString(" = ")
		if op != 0 {
			// p.x = p.x - (_swz).x
			b.WriteString(base)
			b.WriteString(".")
			b.WriteString(comp)
			b.WriteString(" ")
			b.WriteByte(op)
			b.WriteString(" ")
		}
		b.WriteString("(_swz).")
		b.WriteString(comp)
		b.WriteString(";")
	}
	b.WriteString(" }")
	return b.String(), true
}

// stripDeclHead removes the leading declaration keywords from a statement,
// returning the remainder starting at the variable name. With constDecl it strips
// a leading `const` then the type keyword; otherwise it strips only the type
// keyword. e.g. "vec3 q = abs(p)" -> "q = abs(p)".
func stripDeclHead(s string, constDecl bool) string {
	s = strings.TrimSpace(s)
	if constDecl {
		s = strings.TrimSpace(strings.TrimPrefix(s, "const"))
	}
	// Strip the single leading type keyword token.
	for kw := range glslDeclTypes {
		if strings.HasPrefix(s, kw) && len(s) > len(kw) && isSpace(s[len(kw)]) {
			return strings.TrimSpace(s[len(kw):])
		}
	}
	return s
}

// rewriteBracelessIf converts a braceless GLSL `if` into a WGSL `if` with a
// braced body. The input is a single `;`-stripped statement beginning with `if`.
// It locates the closing ')' of the condition by paren-matching, then wraps the
// remaining controlled statement in `{ ... ; }`. WGSL also wants a space between
// the condition and the block, which the formatting provides.
func rewriteBracelessIf(s string) string {
	s = strings.TrimSpace(s)
	// Find the '(' that opens the condition.
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return s + ";" // not actually an if-with-condition; leave alone
	}
	depth := 0
	close := -1
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				close = i
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 {
		return s + ";"
	}
	cond := strings.TrimSpace(s[open : close+1]) // includes the parens
	controlled := strings.TrimSpace(s[close+1:])
	if controlled == "" {
		// `if (c) ;` — empty controlled statement; emit an empty block.
		return "if " + cond + " { }"
	}
	return "if " + cond + " { " + controlled + "; }"
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
		"mat4": "mat4x4<f32>",
	}
	return replaceWholeWords(body, repl)
}

// validateIdents enforces a TRUE allowlist over the identifiers in the body: an
// identifier is accepted iff it is a known builtin/keyword, the name of a
// function declared elsewhere in this same program, one of the current
// function's parameters, or a local variable declared in this body. Anything
// else is rejected with an error so the caller falls back to the CPU
// raymarcher. Member/swizzle accesses (e.g. p.x, k.xy) are skipped by
// identifiers(), so swizzle components are never treated as unknown idents.
//
// declared is the set of function names in the whole program; scope is the set
// of parameter and local-variable names visible in this body.
func validateIdents(body string, declared, scope map[string]bool) error {
	for _, id := range identifiers(body) {
		if glslBuiltins[id] || declared[id] || scope[id] {
			continue
		}
		// Synthetic temps emitted by hoistParenSwizzle (`_hs0`, `_hs1`, ...) when
		// lifting a swizzle-of-parenthesized-expression out of an argument.
		if strings.HasPrefix(id, "_hs") {
			continue
		}
		return fmt.Errorf("unsupported GLSL identifier %q (no WGSL builtin, "+
			"declared function, parameter, or local)", id)
	}
	return nil
}

// glslBuiltins is the allowlist of builtin function/identifier names and
// keywords that map 1:1 to WGSL. The vecN/matN constructor names appear both in
// their GLSL form (vec3) and — because rewriteTypeKeywords runs first — in their
// WGSL-rewritten tokenization (vec3<f32> tokenizes as "vec3" then "f32",
// mat3x3<f32> as "mat3x3" then "f32"), so the rewritten spellings are listed too.
var glslBuiltins = map[string]bool{
	// math builtins (1:1 GLSL->WGSL)
	"length": true, "abs": true, "min": true, "max": true, "clamp": true,
	"dot": true, "normalize": true, "sign": true, "sqrt": true, "mix": true,
	"floor": true, "fract": true, "mod": true,
	// type constructors (GLSL spellings)
	"vec2": true, "vec3": true, "vec4": true, "mat3": true, "mat4": true,
	// type tokens left behind by rewriteTypeKeywords
	"f32": true, "mat3x3": true, "mat4x4": true,
	// keywords (incl. the scalar type keyword `float`, which may introduce a
	// local declaration in a body, e.g. `float a = sphere3p(p);`). `var`/`let`
	// are emitted by rewriteStatements when lowering GLSL declarations.
	"return": true, "const": true, "if": true, "float": true,
	"var": true, "let": true,
	// synthetic temporary emitted by expandSwizzleWrite when lowering a
	// multi-component swizzle l-value (p.xy = ...) to per-component writes.
	"_swz": true,
}

// paramNames parses a GLSL param list "<type> <name>, ..." into the set of
// parameter names. A malformed/empty list yields an empty set; malformed params
// are caught separately by rewriteParams.
func paramNames(params string) map[string]bool {
	names := make(map[string]bool)
	params = strings.TrimSpace(params)
	if params == "" {
		return names
	}
	for _, p := range strings.Split(params, ",") {
		fields := strings.Fields(strings.TrimSpace(p))
		if len(fields) == 2 {
			names[fields[1]] = true
		}
	}
	return names
}

// collectLocals scans a function body for local-variable declarations and
// returns the set of declared names. GLSL declares locals as
//
//	<type> <name> = ...;
//	<type> <name>;
//	const <type> <name> = ...;
//
// We detect them by finding a known type keyword used as a declaration (i.e. a
// type token NOT immediately followed by '(' — which would be a constructor
// call — and NOT preceded by '.') and taking the following identifier as the
// declared name.
func collectLocals(body string) map[string]bool {
	locals := make(map[string]bool)
	toks := tokenize(body)
	for i := 0; i < len(toks)-1; i++ {
		if !glslDeclTypes[toks[i]] {
			continue
		}
		// A constructor call like vec3(...) has '(' as the next token, not an
		// identifier; skip those — they are not declarations.
		next := toks[i+1]
		if next == "" || !isIdentStart(next[0]) {
			continue
		}
		// Exclude the type keywords themselves and builtins appearing as the
		// "name" slot (defensive; shouldn't happen in well-formed GLSL).
		if glslDeclTypes[next] {
			continue
		}
		locals[next] = true
	}
	return locals
}

// glslDeclTypes are the GLSL type keywords that can introduce a local variable
// declaration in a body.
var glslDeclTypes = map[string]bool{
	"float": true, "vec2": true, "vec3": true, "vec4": true,
	"mat3": true, "mat4": true,
}

// tokenize splits s into identifier tokens and single-character punctuation
// tokens, dropping whitespace. Identifier tokens preceded by '.' are emitted as
// the bare member name; callers that care about member access can inspect the
// preceding punctuation token. This is intentionally simple — enough to locate
// "<type> <name>" declaration pairs.
func tokenize(s string) []string {
	var toks []string
	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		if isSpace(c) {
			i++
			continue
		}
		if isIdentStart(c) {
			start := i
			for i < n && isIdentChar(s[i]) {
				i++
			}
			toks = append(toks, s[start:i])
			continue
		}
		toks = append(toks, string(c))
		i++
	}
	return toks
}

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
//
// It assumes a non-identifier character follows the fractional part of a
// trailing-dot literal: GLSL has no "3.foo" form (a member access after a number
// is not valid GLSL), so the function does not special-case an identifier
// immediately following the dot.
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
