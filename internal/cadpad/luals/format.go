package luals

import (
	"fmt"
	"strings"

	"github.com/NimbleMarkets/ds4go/lsp"
)

// formatDiags renders diagnostics as "path:line:col: [severity] message" lines.
// It returns "" when there are no diagnostics. label is the human-facing file
// name shown in each line (the workspace-relative path).
func formatDiags(label string, diags []lsp.Diagnostic) string {
	if len(diags) == 0 {
		return ""
	}
	var b strings.Builder
	for i, d := range diags {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s:%d:%d: [%s] %s", label, d.Line, d.Col, d.Severity, d.Message)
	}
	return b.String()
}
