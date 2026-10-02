package main

import (
	"strings"
	"testing"
)

const validMiniSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">
<rect width="10" height="10" fill="#ccc"/>
</svg>`

// XML-valid but rejected by the oksvg rasterizer (bad color literal —
// reproduces "color string ccc.5 is not length 3 or 6").
const badColorSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">
<rect width="10" height="10" fill="#fff"/>
<rect width="5" height="5" fill="#ccc.5"/>
</svg>`

func TestValidateSVGValid(t *testing.T) {
	if got := validateSVG([]byte(validMiniSVG)); got != "valid" {
		t.Errorf("validateSVG = %q, want %q", got, "valid")
	}
}

// TestValidateSVGRenderError verifies the gate rejects documents the
// rasterizer cannot render, even when the XML is well-formed. Regression
// test: "color string ccc.5 is not length 3 or 6" used to surface only as
// a post-hoc viewer warning the agent never saw.
func TestValidateSVGRenderError(t *testing.T) {
	got := validateSVG([]byte(badColorSVG))
	if !strings.HasPrefix(got, "render error:") {
		t.Errorf("validateSVG = %q, want render error prefix", got)
	}
	if !strings.Contains(got, "ccc.5") {
		t.Errorf("validateSVG = %q, want mention of the bad color literal", got)
	}
}

func TestValidateSVGParseError(t *testing.T) {
	got := validateSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect</svg>`))
	if !strings.HasPrefix(got, "parse error:") {
		t.Errorf("validateSVG = %q, want parse error prefix", got)
	}
}

func TestValidateSVGDetailedRenderError(t *testing.T) {
	got := validateSVGDetailed(badColorSVG)
	if !strings.HasPrefix(got, "Invalid:") {
		t.Fatalf("validateSVGDetailed = %q, want Invalid prefix", got)
	}
	if !strings.Contains(got, "ccc.5") {
		t.Errorf("report %q does not mention the bad color literal", got)
	}
	// The bad literal is on line 3; the report should point the agent there.
	if !strings.Contains(got, "line 3") {
		t.Errorf("report %q does not locate the error at line 3", got)
	}
	if !strings.Contains(got, `3 | <rect width="5" height="5" fill="#ccc.5"/>`) {
		t.Errorf("report %q does not include a numbered snippet of line 3", got)
	}
}

func TestValidateSVGDetailedXMLErrorHasSnippet(t *testing.T) {
	bad := "<svg xmlns=\"http://www.w3.org/2000/svg\">\n<text>a & b</text>\n</svg>"
	got := validateSVGDetailed(bad)
	if !strings.HasPrefix(got, "Invalid:") {
		t.Fatalf("validateSVGDetailed = %q, want Invalid prefix", got)
	}
	if !strings.Contains(got, "2 | <text>a & b</text>") {
		t.Errorf("report %q does not include a numbered snippet of the error line", got)
	}
}

func TestValidateSVGDetailedValid(t *testing.T) {
	got := validateSVGDetailed(validMiniSVG)
	if !strings.HasPrefix(got, "Valid:") {
		t.Errorf("validateSVGDetailed = %q, want Valid prefix", got)
	}
}

func TestNumberedLines(t *testing.T) {
	content := "a\nb\nc\nd\ne"
	got := numberedLines(content, 2, 4)
	want := "  2 | b\n  3 | c\n  4 | d"
	if got != want {
		t.Errorf("numberedLines(2,4) = %q, want %q", got, want)
	}
}

func TestNumberedLinesClampsRange(t *testing.T) {
	content := "a\nb\nc"
	got := numberedLines(content, -5, 99)
	want := "  1 | a\n  2 | b\n  3 | c"
	if got != want {
		t.Errorf("numberedLines(-5,99) = %q, want %q", got, want)
	}
}

func TestErrorLine(t *testing.T) {
	cases := []struct {
		msg  string
		want int
	}{
		{"parse error: XML syntax error on line 224: invalid XML name: 10", 224},
		{"XML parse error at line 36: invalid character entity", 36},
		{"render error: color string ccc.5 is not length 3 or 6", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := errorLine(c.msg); got != c.want {
			t.Errorf("errorLine(%q) = %d, want %d", c.msg, got, c.want)
		}
	}
}

func TestLocateRenderErrorLine(t *testing.T) {
	// Token "ccc.5" (contains digit/punctuation) is on line 3.
	if got := locateRenderErrorLine(badColorSVG, "color string ccc.5 is not length 3 or 6"); got != 3 {
		t.Errorf("locateRenderErrorLine = %d, want 3", got)
	}
	// No literal-looking token present in the document.
	if got := locateRenderErrorLine(validMiniSVG, "something went wrong"); got != 0 {
		t.Errorf("locateRenderErrorLine = %d, want 0", got)
	}
}

func TestAutoCorrectFeedbackDraftError(t *testing.T) {
	v := "parse error: XML syntax error on line 3: invalid XML name: 10"
	got := autoCorrectFeedback(v, []byte(badColorSVG), true)
	if !strings.Contains(got, "3 | ") {
		t.Errorf("feedback %q lacks a numbered snippet around line 3", got)
	}
	if !strings.Contains(got, "do NOT call svg_clear") {
		t.Errorf("feedback %q lacks the minimal-edit instruction", got)
	}
}

func TestAutoCorrectFeedbackRenderErrorLocatesLine(t *testing.T) {
	v := "render error: color string ccc.5 is not length 3 or 6"
	got := autoCorrectFeedback(v, []byte(badColorSVG), true)
	if !strings.Contains(got, `3 | <rect width="5" height="5" fill="#ccc.5"/>`) {
		t.Errorf("feedback %q does not locate the bad color line", got)
	}
}

func TestAutoCorrectFeedbackEmptyDraft(t *testing.T) {
	got := autoCorrectFeedback("no SVG markup found in the draft file or in your response", nil, false)
	if !strings.Contains(got, "svg_append") {
		t.Errorf("feedback %q should instruct rebuilding with svg_append", got)
	}
	if strings.Contains(got, "svg_replace_lines") {
		t.Errorf("feedback %q should not suggest line edits on an empty draft", got)
	}
}

func TestCheckColorLiterals(t *testing.T) {
	wrap := func(attrs string) []byte {
		return []byte(`<svg xmlns="http://www.w3.org/2000/svg"><defs><linearGradient id="g"><stop ` + attrs + `/></linearGradient></defs><g ` + attrs + `/></svg>`)
	}
	for _, tc := range []struct {
		name, attrs, wantErr string // wantErr "" means accepted
	}{
		{"hex6", `fill="#a1b2c3"`, ""},
		{"hex3", `stroke="#abc"`, ""},
		{"named", `fill="navy"`, ""},
		{"unknown name", `fill="rebeccapurple"`, "rebeccapurple"}, // CSS4, not an SVG 1.1 name
		{"rgba", `fill="rgba(1,2,3,0.5)"`, ""},
		{"none and keywords", `fill="none" stroke="currentColor"`, ""},
		{"inherit", `fill="inherit"`, ""},
		{"paint server", `fill="url(#g)"`, ""},
		{"valid style", `style="fill:#fff; stroke : red;stroke-width:2"`, ""},
		{"bad fill attr", `fill="#ccc.5"`, "ccc.5"},
		{"bad stroke attr", `stroke="#12"`, "stroke"},
		{"bad stop-color", `stop-color="#zz"`, "stop-color"},
		{"bad style fill", `style="stroke-width:2;fill:#ccc.5"`, "ccc.5"},
		{"bad style case", `style="FILL:#ccc.5"`, "ccc.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkColorLiterals(wrap(tc.attrs))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("error = %v, want mention of %q", err, tc.wantErr)
			}
		})
	}
}

// Other elements, such as <metadata>, must not make a document invalid.
func TestValidateSVGToleratesUnknownElements(t *testing.T) {
	doc := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><metadata><x/></metadata><rect width="5" height="5" fill="red"/></svg>`
	if got := validateSVG([]byte(doc)); got != "valid" {
		t.Errorf("validateSVG = %q, want valid", got)
	}
}
