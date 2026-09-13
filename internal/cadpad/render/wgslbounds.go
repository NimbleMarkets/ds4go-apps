package render

import "strings"

// GLSL permits max(vec3, float), but WGSL min/max/clamp require matching
// argument types. Metal tolerated the unsplatted form; Vulkan's SPIR-V path
// passed mixed types to the driver and failed pipeline compilation.
func splatVectorBounds(body, params string) string {
	vecs := make(map[string]string)
	toks := tokenize(params + ";" + body)
	for i := 0; i+1 < len(toks); i++ {
		if isVectorType(toks[i]) && isIdentStart(toks[i+1][0]) {
			vecs[toks[i+1]] = toks[i]
		}
	}
	return rewriteBoundsCalls(body, vecs)
}

func isVectorType(s string) bool {
	return s == "vec2" || s == "vec3" || s == "vec4"
}

// boundsVectorType recognizes the vector arguments emitted by gsdf: local
// variables, parameter swizzles, and explicit vector constructors.
func boundsVectorType(expr string, vecs map[string]string) string {
	toks := tokenize(strings.TrimSpace(expr))
	if len(toks) == 1 {
		return vecs[toks[0]]
	}
	if len(toks) == 3 && toks[1] == "." && vecs[toks[0]] != "" {
		if n := len(toks[2]); n >= 2 && n <= 4 {
			return "vec" + string(byte('0'+n))
		}
	}
	if len(toks) > 2 && isVectorType(toks[0]) && toks[1] == "(" {
		return toks[0]
	}
	return ""
}

func rewriteBoundsCalls(s string, vecs map[string]string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if !isIdentStart(s[i]) {
			out.WriteByte(s[i])
			i++
			continue
		}
		start := i
		for i < len(s) && isIdentChar(s[i]) {
			i++
		}
		name := s[start:i]
		open := i
		for open < len(s) && isSpace(s[open]) {
			open++
		}
		if (name != "max" && name != "min" && name != "clamp") || open == len(s) || s[open] != '(' {
			out.WriteString(s[start:i])
			continue
		}
		depth, end, argStart := 1, open+1, open+1
		var args []string
		for ; end < len(s); end++ {
			switch s[end] {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 1 {
					args = append(args, s[argStart:end])
					argStart = end + 1
				}
			}
			if depth == 0 {
				break
			}
		}
		if depth != 0 {
			out.WriteString(s[start:])
			break
		}
		args = append(args, s[argStart:end])
		for j := range args {
			args[j] = rewriteBoundsCalls(args[j], vecs)
		}
		if len(args) >= 2 {
			if typ := boundsVectorType(args[0], vecs); typ != "" {
				for j := 1; j < len(args); j++ {
					// Vector constructors also accept a vector unchanged, so this
					// is valid for both scalar and vector bounds.
					args[j] = typ + "(" + strings.TrimSpace(args[j]) + ")"
				}
			}
		}
		out.WriteString(s[start : open+1])
		out.WriteString(strings.Join(args, ","))
		out.WriteByte(')')
		i = end + 1
	}
	return out.String()
}
