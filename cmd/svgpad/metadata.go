package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	svg "github.com/NimbleMarkets/ntcharts-svg/svg"
)

// metadataSessionCtx is the per-session context budget for the metadata
// pass. The prompt template + a typical SVG/preview + the model's reply fit well
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
	filename       string
	title          string
	desc           string
	keywords       string
	model          string
	generatedAt    time.Time
	genTime        time.Duration
	toolCalls      []toolCallEntry
	vision         bool
	previewWarning string
	err            error
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
	think       string
	model       string
	generatedAt time.Time
	genTime     time.Duration
	toolCalls   []toolCallEntry
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
		think:    xmlUnescape(innerText(svg, "<ai:think>", "</ai:think>")),
		model:    xmlUnescape(innerText(svg, "<ai:model>", "</ai:model>")),
	}
	if ts := innerText(svg, "<ai:generatedAt>", "</ai:generatedAt>"); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			m.generatedAt = t
		}
	}
	if gts := innerText(svg, "<ai:genTime>", "</ai:genTime>"); gts != "" {
		if d, err := time.ParseDuration(gts); err == nil {
			m.genTime = d
		}
	}
	m.toolCalls = parseSVGToolCalls(svg)
	return m
}

func parseSVGToolCalls(svg string) []toolCallEntry {
	var list []toolCallEntry
	content := innerText(svg, "<ai:toolCalls>", "</ai:toolCalls>")
	if content == "" {
		return nil
	}
	remaining := content
	for {
		tcBlock := innerText(remaining, "<ai:toolCall>", "</ai:toolCall>")
		if tcBlock == "" {
			break
		}
		roundVal := 0
		if rs := innerText(tcBlock, "<ai:round>", "</ai:round>"); rs != "" {
			fmt.Sscanf(rs, "%d", &roundVal)
		}
		list = append(list, toolCallEntry{
			round:  roundVal,
			name:   xmlUnescape(innerText(tcBlock, "<ai:name>", "</ai:name>")),
			args:   xmlUnescape(innerText(tcBlock, "<ai:args>", "</ai:args>")),
			result: xmlUnescape(innerText(tcBlock, "<ai:result>", "</ai:result>")),
		})
		idx := strings.Index(remaining, "</ai:toolCall>")
		if idx == -1 {
			break
		}
		remaining = remaining[idx+len("</ai:toolCall>"):]
	}
	return list
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

func buildMetadataBlock(title, desc, keywords, prompt, model string, now time.Time, genTime time.Duration, toolCalls []toolCallEntry, think string) string {
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
	if think != "" {
		fmt.Fprintf(&b, "        <ai:think>%s</ai:think>\n", xmlEscape(think))
	}
	fmt.Fprintf(&b, "        <ai:model>%s</ai:model>\n", xmlEscape(model))
	b.WriteString("        <ai:provider>local ds4</ai:provider>\n")
	fmt.Fprintf(&b, "        <ai:generatedAt>%s</ai:generatedAt>\n", ts)
	if genTime > 0 {
		fmt.Fprintf(&b, "        <ai:genTime>%s</ai:genTime>\n", genTime.String())
	}
	if len(toolCalls) > 0 {
		b.WriteString("        <ai:toolCalls>\n")
		for _, tc := range toolCalls {
			b.WriteString("          <ai:toolCall>\n")
			fmt.Fprintf(&b, "            <ai:round>%d</ai:round>\n", tc.round)
			fmt.Fprintf(&b, "            <ai:name>%s</ai:name>\n", xmlEscape(tc.name))
			fmt.Fprintf(&b, "            <ai:args>%s</ai:args>\n", xmlEscape(tc.args))
			fmt.Fprintf(&b, "            <ai:result>%s</ai:result>\n", xmlEscape(tc.result))
			b.WriteString("          </ai:toolCall>\n")
		}
		b.WriteString("        </ai:toolCalls>\n")
	}
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
func renderEntryInfo(e svgEntry, doc *svg.Document, width int) string {
	var b strings.Builder
	b.WriteString(infoHeadStyle.Render("Image"))
	b.WriteByte('\n')

	row := func(label, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(&b, "  %-7s %s\n", label, value)
	}

	rowRight := func(label, valStr string) {
		leftPadding := "  "
		rightMargin := "  "
		avail := width - len(leftPadding) - len(rightMargin)
		if avail < 10 {
			avail = 10
		}

		lbl := label
		valLen := len(valStr)
		if len(lbl)+1+valLen > avail {
			maxLblLen := avail - valLen - 1
			if maxLblLen > 3 {
				lbl = lbl[:maxLblLen-3] + "..."
			} else {
				lbl = lbl[:maxLblLen]
			}
		}

		padLen := avail - len(lbl) - valLen
		if padLen < 1 {
			padLen = 1
		}
		padding := strings.Repeat(" ", padLen)

		fmt.Fprintf(&b, "%s%s%s%s%s\n", leftPadding, lbl, padding, valStr, rightMargin)
	}

	row("file", e.filename)
	row("model", e.model)
	if !e.generatedAt.IsZero() {
		row("when", e.generatedAt.Local().Format("2006-01-02 15:04"))
	}
	if e.genTime > 0 {
		row("gen time", fmtDuration(e.genTime))
	}
	row("tags", e.keywords)

	if len(e.toolCalls) > 0 {
		b.WriteByte('\n')
		b.WriteString(infoHeadStyle.Render("Tool Calls"))
		b.WriteByte('\n')
		var maxRound int
		for _, tc := range e.toolCalls {
			if tc.round > maxRound {
				maxRound = tc.round
			}
		}
		rowRight("calls", fmt.Sprintf("%d", len(e.toolCalls)))
		rowRight("rounds", fmt.Sprintf("%d", maxRound+1))
	}

	if doc != nil {
		b.WriteByte('\n')
		b.WriteString(infoHeadStyle.Render("SVG Elements"))
		b.WriteByte('\n')

		total := fmt.Sprintf("%d", doc.TotalElements())
		if doc.CountCapped() {
			total += "+"
		}
		rowRight("total", total)

		hist := doc.Histogram()
		for _, item := range hist {
			rowRight(item.Name, fmt.Sprintf("%d", item.Count))
		}
	}

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

// The markup remains useful context, but a vision model also sees the actual
// saved drawing. A rendering failure leaves a usable text-only message.
func metadataUserMessage(prompt string, svgData []byte, vision bool) (ds4.ChatMessage, error) {
	msg := ds4.ChatMessage{Role: "user", Content: fmt.Sprintf("Original prompt:\n%s\n\nSVG markup:\n%s", prompt, svgData)}
	if !vision {
		return msg, nil
	}
	if _, err := inspectSVGDocument(string(svgData), false); err != nil {
		return msg, fmt.Errorf("metadata preview: %w", err)
	}
	img, err := renderVisualPreview(svgData)
	if err != nil {
		return msg, err
	}
	msg.Parts = []ds4.ContentPart{
		{Text: msg.Content + "\n\nThe attached image is this SVG rendered on white. Describe the visible result; the original prompt is context, not proof that a requested element is present."},
		{Image: &img},
	}
	return msg, nil
}

func enrichMetadataCmd(ctx context.Context, wg *sync.WaitGroup, eng *ds4.Engine, modelName, filename, prompt string, svgData []byte, genTime time.Duration, toolCalls []toolCallEntry, think string) tea.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	// The UI can begin another request while enrichment runs. Snapshot inputs
	// before it reuses its draft/tool slices.
	svgData = append([]byte(nil), svgData...)
	toolCalls = append([]toolCallEntry(nil), toolCalls...)
	if wg != nil {
		wg.Add(1)
	}
	return func() tea.Msg {
		if wg != nil {
			defer wg.Done()
		}
		base := metadataDoneMsg{filename: filename, model: modelName}
		fail := func(err error) metadataDoneMsg { base.err = err; return base }
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if eng == nil {
			return fail(fmt.Errorf("metadata requires a loaded engine"))
		}
		sess, err := eng.NewSession(metadataSessionCtx)
		if err != nil {
			return fail(fmt.Errorf("metadata session: %w", err))
		}
		defer sess.Close()

		userMsg, previewErr := metadataUserMessage(prompt, svgData, eng.HasVision())
		if previewErr != nil {
			base.previewWarning = previewErr.Error()
		}
		var images *ds4.ImageEncoder
		base.vision = len(userMsg.Parts) > 0
		if base.vision {
			images = ds4.NewImageEncoder(eng)
			defer images.SetLimits(0, 0) // release embeddings before WG.Done permits engine shutdown
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		input, err := ds4.BuildChatPromptMultimodal(eng, images, metadataSystemPrompt, nil, []ds4.ChatMessage{userMsg}, ds4.ThinkNone)
		if err != nil {
			return fail(err)
		}
		defer input.Free()

		var buf []byte
		opts := ds4.GenerateOptions{
			MaxTokens: metadataMaxTokens,
			StopOnEOS: true,
			Context:   ctx,
		}
		opts.OnToken = func(token int) {
			if text, err := eng.TokenText(token); err == nil {
				buf = append(buf, text...)
			}
		}
		gen := ds4.Generator{Engine: eng, Session: sess}
		if _, err := gen.GeneratePrompt(input, opts); err != nil {
			return fail(err)
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}

		title, desc, keywords := parseLLMMetadata(string(buf))
		if title == "" && desc == "" {
			return fail(fmt.Errorf("no <title>/<desc> in model output"))
		}
		generatedAt := time.Now()
		block := buildMetadataBlock(title, desc, keywords, prompt, modelName, generatedAt, genTime, toolCalls, think)

		base.title, base.desc, base.keywords = title, desc, keywords
		base.generatedAt, base.genTime, base.toolCalls = generatedAt, genTime, toolCalls
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
