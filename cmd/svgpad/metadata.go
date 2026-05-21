package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
)

// metadataSessionCtx is the per-session context budget for the metadata
// pass. The prompt template + a typical SVG + the model's reply fit well
// inside this without competing with the main session's context.
const metadataSessionCtx = 8192

// metadataMaxTokens caps how much the model can produce on the metadata
// turn — well above what the requested XML normally needs.
const metadataMaxTokens = 1024

// metadataSystemPrompt is the system prompt sent on the second LLM pass.
// The model is only asked for <title>/<desc>/<keywords>; everything else
// in the final block is assembled in Go from values we already know.
//
//go:embed metadata_prompt.txt
var metadataSystemPrompt string

// metadataDoneMsg is delivered when the post-save enrichment goroutine
// finishes. Fields that are populated even on error (title/desc/keywords)
// come from the LLM; model/generatedAt are the deterministic values Go
// stamped into the saved metadata block. Errors are non-fatal because
// the SVG itself is already saved and valid.
type metadataDoneMsg struct {
	filename    string
	title       string
	desc        string
	keywords    string
	model       string
	generatedAt time.Time
	err         error
}

// parseLLMMetadata tolerantly extracts <title>, <desc>, and <keywords>
// from raw model output. Missing fields yield empty strings; preambles
// and ```xml fences are ignored because we only look at element bodies.
func parseLLMMetadata(raw string) (title, desc, keywords string) {
	title = innerText(raw, "<title>", "</title>")
	desc = innerText(raw, "<desc>", "</desc>")
	keywords = innerText(raw, "<keywords>", "</keywords>")
	return
}

// svgFileMetadata is the subset of a saved SVG's <title>/<desc>/<metadata>
// block that we surface in the TUI when re-loading old files. Fields are
// XML-unescaped so prompts that contained < & > render naturally.
type svgFileMetadata struct {
	title       string
	desc        string
	keywords    string
	prompt      string
	model       string
	generatedAt time.Time
}

// parseSVGMetadata pulls our metadata fields out of a previously saved
// SVG. Missing or malformed fields fall back to zero values — old files
// without metadata still work, just without the extra display.
func parseSVGMetadata(svg string) svgFileMetadata {
	m := svgFileMetadata{
		title:    xmlUnescape(innerText(svg, "<title>", "</title>")),
		desc:     xmlUnescape(innerText(svg, "<desc>", "</desc>")),
		keywords: xmlUnescape(innerText(svg, "<dc:subject>", "</dc:subject>")),
		prompt:   xmlUnescape(innerText(svg, "<ai:prompt>", "</ai:prompt>")),
		model:    xmlUnescape(innerText(svg, "<ai:model>", "</ai:model>")),
	}
	if ts := innerText(svg, "<ai:generatedAt>", "</ai:generatedAt>"); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			m.generatedAt = t
		}
	}
	return m
}

func innerText(s, open, close string) string {
	_, after, ok := strings.Cut(s, open)
	if !ok {
		return ""
	}
	inner, _, ok := strings.Cut(after, close)
	if !ok {
		return ""
	}
	return strings.TrimSpace(inner)
}

// buildMetadataBlock assembles the <title>/<desc>/<metadata> region that
// gets spliced into the SVG. The only values the LLM contributes are
// title, desc, and keywords — prompt, model, and timestamp come straight
// from Go, so they cannot be paraphrased, mistimed, or hallucinated.
// All text content is XML-escaped.
func buildMetadataBlock(title, desc, keywords, prompt, model string, now time.Time) string {
	if title == "" && desc == "" {
		return ""
	}
	date := now.Format("2006-01-02")
	ts := now.UTC().Format(time.RFC3339)

	var b strings.Builder
	if title != "" {
		fmt.Fprintf(&b, "<title>%s</title>\n", xmlEscape(title))
	}
	if desc != "" {
		fmt.Fprintf(&b, "<desc>%s</desc>\n", xmlEscape(desc))
	}
	b.WriteString("<metadata>\n")
	b.WriteString("  <rdf:RDF xmlns:rdf=\"http://www.w3.org/1999/02/22-rdf-syntax-ns#\"\n")
	b.WriteString("           xmlns:dc=\"http://purl.org/dc/elements/1.1/\"\n")
	b.WriteString("           xmlns:ai=\"https://svgpad.local/ns/ai\">\n")
	b.WriteString("    <rdf:Description about=\"\">\n")
	if title != "" {
		fmt.Fprintf(&b, "      <dc:title>%s</dc:title>\n", xmlEscape(title))
	}
	b.WriteString("      <dc:creator>svgpad</dc:creator>\n")
	if desc != "" {
		fmt.Fprintf(&b, "      <dc:description>%s</dc:description>\n", xmlEscape(desc))
	}
	if keywords != "" {
		fmt.Fprintf(&b, "      <dc:subject>%s</dc:subject>\n", xmlEscape(keywords))
	}
	fmt.Fprintf(&b, "      <dc:date>%s</dc:date>\n", date)
	b.WriteString("      <ai:generation>\n")
	fmt.Fprintf(&b, "        <ai:prompt>%s</ai:prompt>\n", xmlEscape(prompt))
	fmt.Fprintf(&b, "        <ai:model>%s</ai:model>\n", xmlEscape(model))
	b.WriteString("        <ai:provider>local ds4</ai:provider>\n")
	fmt.Fprintf(&b, "        <ai:generatedAt>%s</ai:generatedAt>\n", ts)
	b.WriteString("      </ai:generation>\n")
	b.WriteString("    </rdf:Description>\n")
	b.WriteString("  </rdf:RDF>\n")
	b.WriteString("</metadata>")
	return b.String()
}

// xmlEscape escapes the five reserved XML characters. We avoid
// encoding/xml.EscapeText because it also escapes whitespace as numeric
// char refs, which would mangle the multi-line <desc>/<ai:prompt> bodies
// we want to keep human-readable in the saved SVG.
var xmlReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func xmlEscape(s string) string { return xmlReplacer.Replace(s) }

// xmlUnescape reverses xmlEscape. &amp; is last so we don't double-
// unescape sequences like "&amp;lt;" — the literal "&lt;" that user
// content contained originally must survive a round trip.
var xmlUnescaper = strings.NewReplacer(
	"&apos;", "'",
	"&quot;", `"`,
	"&gt;", ">",
	"&lt;", "<",
	"&amp;", "&",
)

func xmlUnescape(s string) string { return xmlUnescaper.Replace(s) }

// entryText composes what the thinking/output panel shows for an
// svgEntry — the prompt that produced it, then the enrichment title and
// description, then any free-form text the model emitted outside the
// SVG. Empty fields are skipped.
func entryText(e svgEntry) string {
	var b strings.Builder
	if e.prompt != "" {
		b.WriteString(infoHeadStyle.Render("PROMPT"))
		b.WriteByte('\n')
		b.WriteString(e.prompt)
		b.WriteString("\n\n")
	}
	if e.title != "" {
		b.WriteString(titleStyle.Render(e.title))
		b.WriteString("\n\n")
	}
	if e.desc != "" {
		b.WriteString(e.desc)
		b.WriteByte('\n')
	}
	if e.text != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.text)
	}
	return b.String()
}

// renderEntryInfo returns the body of the small left-hand info panel for
// the currently-shown entry. Width is the content width (the caller
// supplies the border). Empty fields are skipped so the panel collapses
// gracefully when an old file has no metadata.
func renderEntryInfo(e svgEntry) string {
	var b strings.Builder
	b.WriteString(infoHeadStyle.Render("Image"))
	b.WriteByte('\n')

	row := func(label, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(&b, "  %-7s %s\n", label, value)
	}

	row("file", e.filename)
	row("model", e.model)
	if !e.generatedAt.IsZero() {
		row("when", e.generatedAt.Local().Format("2006-01-02 15:04"))
	}
	row("tags", e.keywords)
	return b.String()
}

// spliceMetadataIntoSVG injects block right after the opening <svg ...>
// tag of svg, preserving the rest of the document. Returns svg unchanged
// when block is empty or the SVG root is self-closing (nowhere to put
// children).
func spliceMetadataIntoSVG(svg, block string) string {
	if block == "" {
		return svg
	}
	open := strings.Index(svg, "<svg")
	if open < 0 {
		return svg
	}
	closeTag := strings.Index(svg[open:], ">")
	if closeTag < 0 {
		return svg
	}
	closeTag += open
	// Self-closing root: <svg .../>  — the rune just before '>' is '/'.
	if closeTag > 0 && svg[closeTag-1] == '/' {
		return svg
	}
	return svg[:closeTag+1] + "\n" + block + "\n" + svg[closeTag+1:]
}

// enrichMetadataCmd opens a fresh ds4 session, asks the model for a
// title/desc/keywords trio, assembles a full deterministic metadata
// block around those values, and splices it into the file on disk.
// Failures are non-fatal: the SVG is already valid without metadata.
func enrichMetadataCmd(eng *ds4.Engine, modelName, filename, prompt string, svgData []byte) tea.Cmd {
	return func() tea.Msg {
		sess, err := eng.NewSession(metadataSessionCtx)
		if err != nil {
			return metadataDoneMsg{filename: filename, err: fmt.Errorf("metadata session: %w", err)}
		}
		defer sess.Close()

		tokens, err := eng.NewTokens(nil)
		if err != nil {
			return metadataDoneMsg{filename: filename, err: err}
		}
		defer tokens.Free()

		userMsg := fmt.Sprintf("Original prompt:\n%s\n\nSVG markup:\n%s",
			prompt, string(svgData))

		for _, step := range []func() error{
			func() error { return eng.ChatBegin(tokens) },
			func() error { return eng.ChatAppendMessage(tokens, "system", metadataSystemPrompt) },
			func() error { return eng.ChatAppendMessage(tokens, "user", userMsg) },
			func() error { return eng.ChatAppendAssistantPrefix(tokens, ds4.ThinkNone) },
		} {
			if err := step(); err != nil {
				return metadataDoneMsg{filename: filename, err: err}
			}
		}

		var buf []byte
		opts := ds4.GenerateOptions{
			MaxTokens: metadataMaxTokens,
			StopOnEOS: true,
			Context:   context.Background(),
		}
		opts.OnToken = func(token int) {
			if text, err := eng.TokenText(token); err == nil {
				buf = append(buf, text...)
			}
		}
		gen := ds4.Generator{Engine: eng, Session: sess}
		if _, err := gen.GenerateTokens(tokens, opts); err != nil {
			return metadataDoneMsg{filename: filename, err: err}
		}

		title, desc, keywords := parseLLMMetadata(string(buf))
		if title == "" && desc == "" {
			return metadataDoneMsg{filename: filename, model: modelName, err: fmt.Errorf("no <title>/<desc> in model output")}
		}
		generatedAt := time.Now()
		block := buildMetadataBlock(title, desc, keywords, prompt, modelName, generatedAt)

		base := metadataDoneMsg{
			filename:    filename,
			title:       title,
			desc:        desc,
			keywords:    keywords,
			model:       modelName,
			generatedAt: generatedAt,
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			base.err = err
			return base
		}
		out := spliceMetadataIntoSVG(string(data), block)
		if err := os.WriteFile(filename, []byte(out), 0644); err != nil {
			base.err = err
			return base
		}
		return base
	}
}
