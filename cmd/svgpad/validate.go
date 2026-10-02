// validate.go: SVG validation for the agent's edit loop. The agent-facing
// validators run the same oksvg/rasterx backend as the viewer, so anything
// that would fail at render time fails inside the loop, with line-targeted
// diagnostics the line-based edit tools can act on.

package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"io"
	"regexp"
	"strings"

	svg "github.com/NimbleMarkets/ntcharts-svg/svg"
	"github.com/NimbleMarkets/oksvg"
)

// renderCheckEdge is the bitmap edge used for validation rasterization.
// Small: parse and style resolution catch the errors; pixel count barely
// matters.
const renderCheckEdge = 64

// colorProperties are the style properties oksvg parses with ParseSVGColor,
// each with the opacity property that carries alpha for it.
var colorProperties = map[string]string{"fill": "fill-opacity", "stroke": "stroke-opacity", "stop-color": "stop-opacity"}

// cssColorDecl finds color declarations in a <style> sheet. The lead-in keeps
// "fill" inside a selector or a longer name (".fill-x", "-fill") from matching.
var cssColorDecl = regexp.MustCompile(`(?i)(?:^|[{;\s])(fill|stroke|stop-color)\s*:\s*([^;}]*)`)

// alphaHex matches the #rgba and #rrggbbaa forms oksvg does not parse.
var alphaHex = regexp.MustCompile(`^#(?:[0-9a-fA-F]{4}|[0-9a-fA-F]{8})$`)

// rasterizeChecked rasterizes like svg.RasterizeSVG and also rejects invalid
// color literals. oksvg now skips an unparsable fill/stroke and keeps the
// inherited paint, so the rasterizer alone reports "ccc.5" as fine; the agent
// would never learn the color it wrote is ignored. Strict parsing is no
// substitute: it also aborts on benign elements such as <metadata>.
func rasterizeChecked(data []byte, maxW, maxH int) (image.Image, error) {
	img, err := svg.RasterizeSVG(data, maxW, maxH)
	if err != nil {
		return nil, err
	}
	if err := checkColorLiterals(data); err != nil {
		return nil, err
	}
	return img, nil
}

// checkColorLiterals returns the first fill, stroke or stop-color value (as an
// attribute, inside style="", or in a <style> sheet) that oksvg cannot parse.
func checkColorLiterals(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	inStyleSheet := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return nil // well-formedness is reported by inspectSVGDocument
		}
		switch t := tok.(type) {
		case xml.CharData:
			if inStyleSheet {
				for _, m := range cssColorDecl.FindAllStringSubmatch(string(t), -1) {
					if err := checkColorValue(strings.ToLower(m[1]), m[2]); err != nil {
						return err
					}
				}
			}
			continue
		case xml.EndElement:
			if t.Name.Local == "style" {
				inStyleSheet = false
			}
			continue
		case xml.StartElement:
			if t.Name.Local == "style" {
				inStyleSheet = true
			}
			if err := checkElementColors(t); err != nil {
				return err
			}
		}
	}
}

// checkElementColors checks the color attributes and style="" declarations of
// one element.
func checkElementColors(start xml.StartElement) error {
	for _, attr := range start.Attr {
		name := strings.ToLower(attr.Name.Local)
		switch {
		case colorProperties[name] != "":
			if err := checkColorValue(name, attr.Value); err != nil {
				return err
			}
		case name == "style":
			for _, decl := range strings.Split(attr.Value, ";") {
				prop, value, found := strings.Cut(decl, ":")
				prop = strings.ToLower(strings.TrimSpace(prop))
				if found && colorProperties[prop] != "" {
					if err := checkColorValue(prop, value); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func checkColorValue(prop, value string) error {
	value, _, _ = strings.Cut(value, "!") // "!important"
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "inherit", "currentcolor", "context-fill", "context-stroke":
		return nil // keywords oksvg resolves before it parses a literal
	}
	if _, err := oksvg.ParseSVGColor(value); err != nil {
		if alphaHex.MatchString(value) {
			return fmt.Errorf("invalid %s %q: alpha hex is not supported; use a 6-digit hex color with %s (or rgba())", prop, value, colorProperties[prop])
		}
		return fmt.Errorf("invalid %s %q: %w", prop, value, err)
	}
	return nil
}

// validateSVG checks well-formedness AND renderability, returning a short
// status string ("valid", or a reason). It rasterizes with the same
// backend as the viewer so the agent's validation gate and the final
// display never disagree.
func validateSVG(data []byte) string {
	if len(data) == 0 {
		return "empty"
	}
	if _, err := inspectSVGDocument(string(data), false); err != nil {
		return "parse error: " + err.Error()
	}
	if _, err := rasterizeChecked(data, renderCheckEdge, renderCheckEdge); err != nil {
		return "render error: " + err.Error()
	}
	return "valid"
}

// validateSVGDetailed performs thorough validation and returns a detailed
// report suitable for feeding back to the LLM via a tool result. Line
// numbers in the report match svg_read output and svg_replace_lines
// arguments.
func validateSVGDetailed(svgStr string) string {
	if strings.TrimSpace(svgStr) == "" {
		return "Error: empty SVG string"
	}
	var issues []string
	if _, err := inspectSVGDocument(svgStr, false); err != nil {
		line := errorLine(err.Error())
		if line == 0 {
			line = 1
		}
		issues = append(issues, fmt.Sprintf("XML parse error at line %d: %v\n%s",
			line, err, numberedSnippet(svgStr, line)))
	}

	// Only consult the rasterizer once the XML is clean; renderer output
	// on malformed XML would just duplicate the parse diagnostics.
	if len(issues) == 0 {
		if _, err := rasterizeChecked([]byte(svgStr), renderCheckEdge, renderCheckEdge); err != nil {
			msg := fmt.Sprintf("Render error from the rasterizer: %v", err)
			if line := locateRenderErrorLine(svgStr, err.Error()); line > 0 {
				msg += fmt.Sprintf(" (likely near line %d)\n%s", line, numberedSnippet(svgStr, line))
			}
			issues = append(issues, msg)
		}
	}

	if len(issues) == 0 {
		return "Valid: well-formed XML with correct SVG root element and namespace; renders successfully."
	}
	return "Invalid:\n" + strings.Join(issues, "\n")
}

// numberedLines renders lines start..end (1-indexed, inclusive, clamped)
// of content with "NNN | " prefixes. The prefix is presentation only — it
// is never part of the file content.
func numberedLines(content string, start, end int) string {
	lines := strings.Split(content, "\n")
	if start < 1 {
		start = 1
	}
	if end < 1 || end > len(lines) {
		end = len(lines)
	}
	if start > end {
		start = end
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		if i > start {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%3d | %s", i, lines[i-1])
	}
	return b.String()
}

// snippetContext is how many lines of context numberedSnippet shows on
// each side of the target line.
const snippetContext = 2

// numberedSnippet returns the numbered lines around line (1-indexed).
func numberedSnippet(content string, line int) string {
	return numberedLines(content, line-snippetContext, line+snippetContext)
}

// autoCorrectFeedback builds the retry message fed back to the model when
// a finished turn produced an invalid or unrenderable SVG. fromDraft says
// whether svgData came from the draft file (line-editable) or was scraped
// from the response text.
func autoCorrectFeedback(v string, svgData []byte, fromDraft bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Your output was invalid: %s.", v)
	doc := string(svgData)
	line := errorLine(v)
	if line == 0 && len(svgData) > 0 {
		line = locateRenderErrorLine(doc, v)
	}
	if line > 0 && fromDraft {
		fmt.Fprintf(&b, "\nDraft around the error (line numbers match svg_read and svg_replace_lines):\n%s",
			numberedSnippet(doc, line))
	}
	switch {
	case len(svgData) == 0:
		b.WriteString("\nThe draft file is empty. Rebuild the SVG in the draft with svg_append, then call svg_validate.")
	case fromDraft:
		b.WriteString("\nMake a minimal fix with svg_replace or svg_replace_lines; do NOT call svg_clear and do NOT regenerate the whole document. Then call svg_validate.")
	default:
		b.WriteString("\nWrite the corrected SVG to the draft with svg_append, then call svg_validate.")
	}
	return b.String()
}

var reErrorLine = regexp.MustCompile(`(?:on|at) line (\d+)`)

// errorLine extracts a 1-indexed line number from a diagnostic message
// ("... on line 224: ..."), or 0 if the message carries none.
func errorLine(msg string) int {
	m := reErrorLine.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	var n int
	fmt.Sscanf(m[1], "%d", &n)
	return n
}

// lineOfOffset converts a byte offset into a 1-indexed line number.
func lineOfOffset(s string, off int) int {
	if off > len(s) {
		off = len(s)
	}
	if off < 0 {
		off = 0
	}
	return 1 + strings.Count(s[:off], "\n")
}

// locateRenderErrorLine guesses which line a rasterizer error refers to.
// Renderer errors (e.g. `color string ccc.5 is not length 3 or 6`) carry
// no position, but they usually quote the offending literal. Tokens that
// look like literals (containing a digit or punctuation) are searched in
// the document, longest first; returns 0 when nothing matches.
func locateRenderErrorLine(content, errMsg string) int {
	tokens := strings.Fields(errMsg)
	var candidates []string
	for _, t := range tokens {
		t = strings.Trim(t, `"'(),:;`)
		if len(t) < 3 {
			continue
		}
		if strings.IndexFunc(t, func(r rune) bool {
			return (r >= '0' && r <= '9') || r == '.' || r == '#' || r == '%' || r == '('
		}) >= 0 {
			candidates = append(candidates, t)
		}
	}
	// Longest first: more specific literals win over generic fragments.
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if len(candidates[j]) > len(candidates[i]) {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	for _, c := range candidates {
		for i, line := range strings.Split(content, "\n") {
			if strings.Contains(line, c) {
				return i + 1
			}
		}
	}
	return 0
}
