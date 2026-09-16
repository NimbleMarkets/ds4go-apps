package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

type svgDocumentState struct {
	rootSeen  bool
	closeLine int // zero until the outermost SVG closes; nested SVGs do not count
}

// inspectSVGDocument enforces a single SVG root. encoding/xml's token reader
// accepts multiple top-level elements, so tokenization alone is insufficient.
// Appending may leave a partial token or open elements; validation may not.
func inspectSVGDocument(doc string, incompleteOK bool) (svgDocumentState, error) {
	var state svgDocumentState
	decoder := xml.NewDecoder(strings.NewReader(doc))
	depth := 0
	for {
		offset := int(decoder.InputOffset())
		tok, err := decoder.Token()
		if err == io.EOF {
			if !state.rootSeen && !incompleteOK {
				return state, fmt.Errorf("Missing <svg root element at line 1")
			}
			return state, nil
		}
		if err != nil {
			if state.closeLine > 0 {
				return state, contentAfterRoot(state.closeLine, lineOfOffset(doc, offset))
			}
			if syntax, ok := err.(*xml.SyntaxError); incompleteOK && ok && syntax.Msg == "unexpected EOF" {
				return state, nil
			}
			return state, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if state.closeLine > 0 {
				return state, contentAfterRoot(state.closeLine, lineOfOffset(doc, offset))
			}
			if !state.rootSeen {
				if t.Name.Local != "svg" {
					return state, fmt.Errorf("Missing <svg root element at line %d; found <%s>", lineOfOffset(doc, offset), t.Name.Local)
				}
				if t.Name.Space != "http://www.w3.org/2000/svg" {
					return state, fmt.Errorf("Missing xmlns=\"http://www.w3.org/2000/svg\" or incorrect namespace on the root at line %d", lineOfOffset(doc, offset))
				}
				state.rootSeen = true
			}
			depth++
		case xml.EndElement:
			depth--
			if depth == 0 {
				state.closeLine = lineOfOffset(doc, offset)
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				if state.closeLine > 0 {
					return state, contentAfterRoot(state.closeLine, lineOfOffset(doc, offset))
				}
				return state, fmt.Errorf("text outside the SVG root at line %d", lineOfOffset(doc, offset))
			}
		}
	}
}

func contentAfterRoot(closed, outside int) error {
	return fmt.Errorf("SVG root closed at line %d, but content follows outside it at line %d. All drawing elements must be inside one <svg>...</svg> root. Use svg_read around the earlier closing line, then svg_replace or svg_replace_lines: move a premature root closure after the drawing, or remove an extra trailing closing tag. Do NOT append another </svg>", closed, outside)
}
