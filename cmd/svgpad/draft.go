package main

import (
	"fmt"
	"os"
	"strings"
)

// appendSVGChunk checks the proposed document before touching the draft.
// Open elements and partial tokens are normal between chunks; content outside
// an already-closed root is not. Refusals preserve the draft byte for byte.
func appendSVGChunk(path, chunk string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read draft: %w", err)
	}
	state, err := inspectSVGDocument(string(data), true)
	if err != nil {
		return fmt.Sprintf("Error: draft unchanged. Repair the existing draft before appending: %v", err), nil
	}
	if state.closeLine > 0 && strings.TrimSpace(chunk) != "" {
		return fmt.Sprintf("Error: draft unchanged; its SVG root is already closed at line %d. Do not append after the root or add another </svg>. Read that line with svg_read, then use svg_replace or svg_replace_lines to insert the new drawing elements INSIDE the existing root, before its closing tag. For a self-closing root, expand it with a replacement first.", state.closeLine), nil
	}
	proposed := string(data) + chunk
	state, err = inspectSVGDocument(proposed, true)
	if err != nil {
		return fmt.Sprintf("Error: draft unchanged; the proposed append is invalid: %v", err), nil
	}
	if err := os.WriteFile(path, []byte(proposed), 0644); err != nil {
		return "", fmt.Errorf("write draft: %w", err)
	}
	if state.closeLine > 0 {
		return fmt.Sprintf("Chunk appended successfully. SVG root is now closed at line %d. Call svg_validate; use replacement tools for further edits inside the root, not svg_append.", state.closeLine), nil
	}
	return "Chunk appended successfully. Draft is still incomplete; continue appending, close the root exactly once when finished, then call svg_validate.", nil
}
