package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const svgOpen = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10">`

func TestValidationPointsToPrematureRootClosure(t *testing.T) {
	// Reproduce the pelican log: the background chunk ends the document,
	// then bicycle and bird elements and more closing tags follow outside it.
	doc := svgOpen + "\n<rect width=\"10\" height=\"10\"/>\n</svg>\n<circle r=\"2\"/>\n</svg>"
	for _, got := range []string{validateSVG([]byte(doc)), validateSVGDetailed(doc)} {
		if !strings.Contains(got, "root closed at line 3") || !strings.Contains(got, "outside it at line 4") || !strings.Contains(got, "Do NOT append another </svg>") {
			t.Fatalf("diagnostic failed to identify earlier root closure: %s", got)
		}
	}
	if got := validateSVGDetailed(doc); !strings.Contains(got, "3 | </svg>") {
		t.Fatalf("missing closing-line snippet: %s", got)
	}
}

func TestSVGDocumentBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		valid     bool
	}{
		{"complete", svgOpen + "</svg>", true},
		{"self closing", `<svg xmlns="http://www.w3.org/2000/svg"/>`, true},
		{"trailing whitespace", svgOpen + "</svg>\n\n", true},
		{"trailing comment", svgOpen + "</svg><!-- </svg><path/> -->", true},
		{"nested svg", svgOpen + "<svg><rect/></svg><circle/></svg>", true},
		{"literal in comment", svgOpen + "<!-- </svg> --><rect/></svg>", true},
		{"literal in cdata", svgOpen + "<desc><![CDATA[</svg>]]></desc></svg>", true},
		{"missing root close", svgOpen + "<rect/>", false},
		{"duplicate close", svgOpen + "</svg></svg>", false},
		{"second root", svgOpen + "</svg>" + svgOpen + "</svg>", false},
		{"sibling shape", svgOpen + "</svg><circle/>", false},
		{"trailing text", svgOpen + "</svg>oops", false},
		{"trailing broken tag", svgOpen + "</svg><", false},
		{"wrong root", `<svgfoo xmlns="http://www.w3.org/2000/svg"/>`, false},
		{"namespace only on child", `<svg><g xmlns="http://www.w3.org/2000/svg"/></svg>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inspectSVGDocument(tc.doc, false)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if !tc.valid {
				if validateSVG([]byte(tc.doc)) == "valid" || strings.HasPrefix(validateSVGDetailed(tc.doc), "Valid:") {
					t.Fatal("validator accepted invalid document")
				}
			}
		})
	}
}

func TestAppendRefusesPostRootChunksWithoutChangingDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.svg")
	background := svgOpen + "\n<rect/>\n</svg>"
	msg, err := appendSVGChunk(path, background)
	if err != nil || !strings.Contains(msg, "root is now closed at line 3") {
		t.Fatalf("%s %v", msg, err)
	}
	for _, chunk := range []string{"<circle/>", "</svg>", "<path/></svg>", svgOpen + "</svg>"} {
		msg, err := appendSVGChunk(path, chunk)
		if err != nil || !strings.Contains(msg, "already closed at line 3") || !strings.Contains(msg, "svg_replace") {
			t.Fatalf("%s %v", msg, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != background {
			t.Fatal("refused append changed the draft")
		}
	}
}

func TestAppendAllowsChunkedConstruction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.svg")
	// Splitting tags and attributes is supported, as are nested SVG roots
	// and closing-tag literals that are not XML structure.
	chunks := []string{`<svg xmlns="http://www.w3.org/`, `2000/svg" viewBox="0 0 10 10">`, "<g><svg><rect", ` width="5"/></svg></g>`, "<!-- </svg> -->", "<circle/>", "</svg>"}
	for _, chunk := range chunks {
		msg, err := appendSVGChunk(path, chunk)
		if err != nil || !strings.HasPrefix(msg, "Chunk appended successfully.") {
			t.Fatalf("chunk %q: %s %v", chunk, msg, err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != strings.Join(chunks, "") || validateSVG(data) != "valid" {
		t.Fatalf("bad assembled draft: %s", data)
	}
}

func TestAppendRejectsBadCandidateBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.svg")
	if _, err := appendSVGChunk(path, svgOpen); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []string{"</svg><circle/>", "<g></svg>", "</svg></svg>"} {
		msg, err := appendSVGChunk(path, chunk)
		if err != nil || !strings.HasPrefix(msg, "Error: draft unchanged") {
			t.Fatalf("%s %v", msg, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != svgOpen {
			t.Fatal("invalid candidate changed draft")
		}
	}
	// Existing invalid drafts must be repaired, not extended.
	bad := svgOpen + "</svg><circle/>"
	if err := os.WriteFile(path, []byte(bad), 0644); err != nil {
		t.Fatal(err)
	}
	msg, err := appendSVGChunk(path, "</svg>")
	if err != nil || !strings.Contains(msg, "Repair the existing draft") {
		t.Fatalf("%s %v", msg, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != bad {
		t.Fatal("invalid existing draft mutated")
	}
}

func TestStructuralDiagnosticsPreserveSourceLineNumbers(t *testing.T) {
	doc := "\n\n" + svgOpen + "\n</svg>\n<circle/>"
	got := validateSVGDetailed(doc)
	if !strings.Contains(got, "root closed at line 4") || !strings.Contains(got, "4 | </svg>") {
		t.Fatalf("incorrect original line numbers: %s", got)
	}
}
