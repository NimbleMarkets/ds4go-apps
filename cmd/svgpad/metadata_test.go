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
		"draw a fox", "qwen.gguf", now,
	)

	// Deterministic Go-supplied fields must appear verbatim, NOT echoed
	// from the LLM.
	mustContain := []string{
		"<dc:date>2026-05-21</dc:date>",
		"<ai:generatedAt>2026-05-21T14:30:00Z</ai:generatedAt>",
		"<ai:prompt>draw a fox</ai:prompt>",
		"<ai:model>qwen.gguf</ai:model>",
		"<ai:provider>local ds4</ai:provider>",
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
	if got := buildMetadataBlock("", "", "k", "p", "m", time.Now()); got != "" {
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
	block := buildMetadataBlock("My Title", "A description.", "a, b, c",
		prompt, "qwen.gguf", now)
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
	if md.model != "qwen.gguf" {
		t.Errorf("model = %q", md.model)
	}
	if !md.generatedAt.Equal(now) {
		t.Errorf("generatedAt = %v, want %v", md.generatedAt, now)
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
