package main

import "strings"

type parseState int

const (
	psInit       parseState = iota
	psFindCanvas            // inside {, scanning for "canvas":
	psOpenCanvas            // found "canvas":, waiting for opening "
	psInCanvas              // reading canvas string value
	psFindDesc              // scanning for "description":
	psOpenDesc              // found "description":, waiting for opening "
	psInDesc                // reading description string value
	psDone
)

// Parser incrementally extracts canvas and description fields from a streaming
// JSON object: {"canvas":"...","description":"..."}.
// Any prefix before the opening { (e.g. a <think>...</think> block) is captured
// into Think.
type Parser struct {
	state   parseState
	buf     []byte
	scanned int
	escape  bool

	Think  []byte
	Canvas []byte
	Desc   []byte
}

func NewParser() *Parser { return &Parser{} }

func (p *Parser) Reset() {
	p.state = psInit
	p.buf = p.buf[:0]
	p.scanned = 0
	p.escape = false
	p.Think = p.Think[:0]
	p.Canvas = p.Canvas[:0]
	p.Desc = p.Desc[:0]
}

func (p *Parser) Feed(s string) {
	p.buf = append(p.buf, s...)
	p.process()
}

// CanvasLines returns the current canvas content split on newlines.
func (p *Parser) CanvasLines() []string {
	if len(p.Canvas) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(p.Canvas), "\n"), "\n")
}

func (p *Parser) DescText() string { return string(p.Desc) }

// ThinkText returns the captured preamble with <think> tags stripped.
func (p *Parser) ThinkText() string {
	s := string(p.Think)
	s = strings.ReplaceAll(s, "<think>", "")
	s = strings.ReplaceAll(s, "</think>", "")
	return strings.TrimSpace(s)
}

func (p *Parser) process() {
	for p.scanned < len(p.buf) && p.state != psDone {
		c := p.buf[p.scanned]
		p.scanned++

		switch p.state {
		case psInit:
			if c == '{' {
				p.state = psFindCanvas
			} else {
				p.Think = append(p.Think, c)
			}

		case psFindCanvas:
			// O(n²) over response size, acceptable since responses are <8KB.
			if strings.HasSuffix(string(p.buf[:p.scanned]), `"canvas":`) {
				p.state = psOpenCanvas
			}

		case psOpenCanvas:
			if c == '"' {
				p.state = psInCanvas
			}

		case psInCanvas:
			if p.escape {
				p.escape = false
				switch c {
				case 'n':
					p.Canvas = append(p.Canvas, '\n')
				case '\\':
					p.Canvas = append(p.Canvas, '\\')
				case '"':
					p.Canvas = append(p.Canvas, '"')
				case 't':
					p.Canvas = append(p.Canvas, '\t')
				case 'r': // discard \r
				default:
					p.Canvas = append(p.Canvas, '\\', c)
				}
			} else if c == '\\' {
				p.escape = true
			} else if c == '"' {
				p.state = psFindDesc
			} else {
				p.Canvas = append(p.Canvas, c)
			}

		case psFindDesc:
			if strings.HasSuffix(string(p.buf[:p.scanned]), `"description":`) {
				p.state = psOpenDesc
			}

		case psOpenDesc:
			if c == '"' {
				p.state = psInDesc
			}

		case psInDesc:
			if p.escape {
				p.escape = false
				switch c {
				case 'n':
					p.Desc = append(p.Desc, '\n')
				case '\\':
					p.Desc = append(p.Desc, '\\')
				case '"':
					p.Desc = append(p.Desc, '"')
				case 't':
					p.Desc = append(p.Desc, '\t')
				case 'r': // discard \r
				default:
					p.Desc = append(p.Desc, '\\', c)
				}
			} else if c == '\\' {
				p.escape = true
			} else if c == '"' {
				p.state = psDone
			} else {
				p.Desc = append(p.Desc, c)
			}
		}
	}
}
