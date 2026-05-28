package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseLLMMetadataClean(t *testing.T) {
	raw := `<title>Sunrise Over Pines</title>
<desc>A serene mountain lake at dawn, mist rising from the water.
Pine trees frame the shoreline.</desc>
<keywords>mountain, lake, dawn, pine, mist</keywords>`
	title, desc, keywords := parseLLMMetadata(raw)
	if title != "Sunrise Over Pines" {
		t.Errorf("title = %q", title)
	}
	if !strings.HasPrefix(desc, "A serene mountain lake") || !strings.Contains(desc, "Pine trees") {
		t.Errorf("desc lost content: %q", desc)
	}
	if keywords != "mountain, lake, dawn, pine, mist" {
		t.Errorf("keywords = %q", keywords)
	}
}

func TestParseLLMMetadataTolerantToFences(t *testing.T) {
	// LLMs sometimes wrap output in code fences or add preamble despite
	// instructions. The parser must still find the elements.
	raw := "Here is the metadata:\n```xml\n<title>Hi</title>\n<desc>D.</desc>\n<keywords>a, b</keywords>\n```\nDone."
	title, desc, kw := parseLLMMetadata(raw)
	if title != "Hi" || desc != "D." || kw != "a, b" {
		t.Errorf("got (%q, %q, %q)", title, desc, kw)
	}
}

func TestParseLLMMetadataMissingFieldsAreEmpty(t *testing.T) {
	title, desc, kw := parseLLMMetadata("no xml here")
	if title != "" || desc != "" || kw != "" {
		t.Errorf("expected all empty, got (%q, %q, %q)", title, desc, kw)
	}
	title, _, _ = parseLLMMetadata("<desc>no title</desc>")
	if title != "" {
		t.Errorf("missing <title> should be empty, got %q", title)
	}
}

func TestBuildMetadataBlockDeterministic(t *testing.T) {
	now := time.Date(2026, 5, 21, 14, 30, 0, 0, time.UTC)
	block := buildMetadataBlock(
		"My Title", "A description.", "a, b, c",
		"draw a fox", "qwen.gguf", now, 12*time.Second, nil, "some thoughts",
	)

	// Deterministic Go-supplied fields must appear verbatim, NOT echoed
	// from the LLM.
	mustContain := []string{
		"<dc:date>2026-05-21</dc:date>",
		"<ai:generatedAt>2026-05-21T14:30:00Z</ai:generatedAt>",
		"<ai:prompt>draw a fox</ai:prompt>",
		"<ai:think>some thoughts</ai:think>",
		"<ai:model>qwen.gguf</ai:model>",
		"<ai:provider>local ds4</ai:provider>",
		"<ai:genTime>12s</ai:genTime>",
		"<dc:creator>svgpad</dc:creator>",
		"<dc:title>My Title</dc:title>",
		"<dc:description>A description.</dc:description>",
		"<dc:subject>a, b, c</dc:subject>",
		"<title>My Title</title>",
		"<desc>A description.</desc>",
	}
	for _, s := range mustContain {
		if !strings.Contains(block, s) {
			t.Errorf("block missing %q\n---\n%s", s, block)
		}
	}
}

func TestBuildMetadataBlockEscapesXML(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	block := buildMetadataBlock(
		`Cats & Dogs <3`,
		`He said "hi" & smiled.`,
		"",
		`<script>alert('x')</script> & friends`,
		"m&m",
		now,
		0,
		nil,
		"",
	)
	// Raw reserved characters must not survive in element bodies.
	for _, bad := range []string{"<script>", "alert('x')", "Cats & Dogs"} {
		if strings.Contains(block, bad) {
			t.Errorf("unescaped %q leaked into block:\n%s", bad, block)
		}
	}
	// Spot-check the escaped forms.
	for _, want := range []string{"&amp;", "&lt;script&gt;", "&quot;hi&quot;", "&apos;x&apos;"} {
		if !strings.Contains(block, want) {
			t.Errorf("missing escaped %q in block:\n%s", want, block)
		}
	}
}

func TestBuildMetadataBlockEmptyWhenNoTitleOrDesc(t *testing.T) {
	if got := buildMetadataBlock("", "", "k", "p", "m", time.Now(), 0, nil, ""); got != "" {
		t.Errorf("expected empty block, got %q", got)
	}
}

func TestSpliceMetadataIntoSVG(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10"/></svg>`
	block := "<title>X</title>\n<desc>Y</desc>\n<metadata/>"
	got := spliceMetadataIntoSVG(svg, block)
	if !strings.Contains(got, `<rect width="10" height="10"/>`) {
		t.Errorf("original content lost: %q", got)
	}
	openIdx := strings.Index(got, ">")
	blockIdx := strings.Index(got, "<title>X</title>")
	rectIdx := strings.Index(got, "<rect")
	if !(openIdx < blockIdx && blockIdx < rectIdx) {
		t.Errorf("block not placed between <svg ...> and first child:\n%q", got)
	}
}

func TestSpliceMetadataPreservesSelfClosingSVG(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"/>`
	got := spliceMetadataIntoSVG(svg, "<title>X</title>")
	if got != svg {
		t.Errorf("self-closing svg should be left alone, got %q", got)
	}
}

func TestParseSVGMetadataRoundTrip(t *testing.T) {
	// What we build into an SVG must come back out matching, including
	// reserved characters that get XML-escaped along the way.
	now := time.Date(2026, 5, 21, 14, 30, 0, 0, time.UTC)
	prompt := `draw a "fox" & a <hound>`
	think := `some <thinking> & "reasoning"`
	toolCalls := []toolCallEntry{
		{round: 0, name: "svg_validate", args: "{}", result: "valid"},
		{round: 1, name: "svg_append", args: `{"code": "<rect/>"}`, result: "success"},
	}
	block := buildMetadataBlock("My Title", "A description.", "a, b, c",
		prompt, "qwen.gguf", now, 15*time.Second, toolCalls, think)
	svg := spliceMetadataIntoSVG(
		`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`, block)

	md := parseSVGMetadata(svg)
	if md.title != "My Title" {
		t.Errorf("title = %q", md.title)
	}
	if md.desc != "A description." {
		t.Errorf("desc = %q", md.desc)
	}
	if md.keywords != "a, b, c" {
		t.Errorf("keywords = %q", md.keywords)
	}
	if md.prompt != prompt {
		t.Errorf("prompt = %q, want %q", md.prompt, prompt)
	}
	if md.think != think {
		t.Errorf("think = %q, want %q", md.think, think)
	}
	if md.model != "qwen.gguf" {
		t.Errorf("model = %q", md.model)
	}
	if !md.generatedAt.Equal(now) {
		t.Errorf("generatedAt = %v, want %v", md.generatedAt, now)
	}
	if md.genTime != 15*time.Second {
		t.Errorf("genTime = %v, want 15s", md.genTime)
	}
	if len(md.toolCalls) != len(toolCalls) {
		t.Errorf("got %d tool calls, want %d", len(md.toolCalls), len(toolCalls))
	} else {
		for i := range toolCalls {
			if md.toolCalls[i] != toolCalls[i] {
				t.Errorf("toolCall[%d] = %+v, want %+v", i, md.toolCalls[i], toolCalls[i])
			}
		}
	}
}

func TestParseSVGMetadataMissingFieldsAreZero(t *testing.T) {
	md := parseSVGMetadata(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`)
	if md.title != "" || md.desc != "" || md.keywords != "" ||
		md.prompt != "" || md.model != "" || !md.generatedAt.IsZero() {
		t.Errorf("expected all zero, got %+v", md)
	}
}

func TestSpliceMetadataIgnoresEmptyBlock(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`
	if got := spliceMetadataIntoSVG(svg, ""); got != svg {
		t.Errorf("empty block should leave svg untouched, got %q", got)
	}
}

func TestExtractThinkAndOutputText(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		thinkActive bool
		wantThink   string
		wantOutput  string
	}{
		{
			name:        "with think tag and closed",
			input:       "<think>reasoning text</think>output text",
			thinkActive: true,
			wantThink:   "reasoning text",
			wantOutput:  "output text",
		},
		{
			name:        "with think tag and unclosed",
			input:       "<think>reasoning text",
			thinkActive: true,
			wantThink:   "reasoning text",
			wantOutput:  "",
		},
		{
			name:        "missing think tag but has end tag (active)",
			input:       "reasoning text</think>output text",
			thinkActive: true,
			wantThink:   "reasoning text",
			wantOutput:  "output text",
		},
		{
			name:        "missing think tag but has end tag (inactive)",
			input:       "reasoning text</think>output text",
			thinkActive: false,
			wantThink:   "",
			wantOutput:  "reasoning text</think>output text",
		},
		{
			name:        "missing think tag and unclosed (active)",
			input:       "reasoning text",
			thinkActive: true,
			wantThink:   "reasoning text",
			wantOutput:  "",
		},
		{
			name:        "missing think tag and unclosed (inactive)",
			input:       "reasoning text",
			thinkActive: false,
			wantThink:   "",
			wantOutput:  "reasoning text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotThink := extractThink(tt.input, tt.thinkActive)
			if gotThink != tt.wantThink {
				t.Errorf("extractThink(%q, %v) = %q, want %q", tt.input, tt.thinkActive, gotThink, tt.wantThink)
			}
			gotOutput := extractOutputText(tt.input, tt.thinkActive)
			if gotOutput != tt.wantOutput {
				t.Errorf("extractOutputText(%q, %v) = %q, want %q", tt.input, tt.thinkActive, gotOutput, tt.wantOutput)
			}
		})
	}
}

func TestValidateSVGDetailed(t *testing.T) {
	tests := []struct {
		name    string
		svg     string
		valid   bool
		wantErr string
	}{
		{
			name:  "valid simple svg",
			svg:   `<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`,
			valid: true,
		},
		{
			name:  "valid simple svg with attributes",
			svg:   `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><g><circle cx="5" cy="5" r="2"/></g></svg>`,
			valid: true,
		},
		{
			name:    "missing svg root",
			svg:     `<g xmlns="http://www.w3.org/2000/svg"></g>`,
			valid:   false,
			wantErr: "Missing <svg root element",
		},
		{
			name:    "missing xmlns",
			svg:     `<svg><rect/></svg>`,
			valid:   false,
			wantErr: "Missing xmlns",
		},
		{
			name:    "unclosed tag",
			svg:     `<svg xmlns="http://www.w3.org/2000/svg"><g><rect/></svg>`,
			valid:   false,
			wantErr: "element <g> closed by </svg>",
		},
		{
			name:    "xml syntax error",
			svg:     `<svg xmlns="http://www.w3.org/2000/svg"><rect x=5/></svg>`,
			valid:   false,
			wantErr: "XML parse error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := validateSVGDetailed(tt.svg)
			if tt.valid {
				if !strings.HasPrefix(res, "Valid:") {
					t.Errorf("expected valid, got: %q", res)
				}
			} else {
				if !strings.HasPrefix(res, "Invalid:") {
					t.Errorf("expected invalid, got: %q", res)
				}
				if !strings.Contains(res, tt.wantErr) {
					t.Errorf("expected error to contain %q, got %q", tt.wantErr, res)
				}
			}
		})
	}
}
