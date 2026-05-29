package luals

import (
	"testing"

	"github.com/NimbleMarkets/ds4go/lsp"
)

func TestFormatDiags_Empty(t *testing.T) {
	if got := formatDiags("a.lua", nil); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestFormatDiags_Lines(t *testing.T) {
	diags := []lsp.Diagnostic{
		{Line: 3, Col: 5, Severity: lsp.SeverityError, Message: "unexpected symbol"},
		{Line: 7, Col: 1, Severity: lsp.SeverityWarning, Message: "unused local 'x'"},
	}
	want := "a.lua:3:5: [error] unexpected symbol\n" +
		"a.lua:7:1: [warning] unused local 'x'"
	if got := formatDiags("a.lua", diags); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}
