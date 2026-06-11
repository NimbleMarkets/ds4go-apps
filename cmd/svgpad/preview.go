// preview.go: live-preview assembly for the streaming generation path.
// The preview follows the draft file: previewBase is the draft content at
// the last round boundary, pending chunks are validated svg_append
// arguments streamed since then (dsml.EventToolCallEnd), and the document
// is auto-closed so the rasterizer can render mid-build state.

package main

import "encoding/json"

// chunkFromArgs extracts the svg_append "chunk" parameter from a tool-call
// arguments JSON object. Returns "" for anything that does not parse.
func chunkFromArgs(args string) string {
	var params struct {
		Chunk string `json:"chunk"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return ""
	}
	return params.Chunk
}

// previewSVG assembles a renderable preview from the draft base plus
// pending svg_append chunks, appending a closing </svg> when the document
// is still open. Returns nil when no <svg root exists yet.
func previewSVG(base string, pending []string) []byte {
	doc := base
	for _, c := range pending {
		doc += c
	}
	return extractIncrementalSVG(doc)
}
