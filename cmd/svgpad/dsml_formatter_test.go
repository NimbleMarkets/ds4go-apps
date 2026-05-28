package main

import (
	"testing"
)

func TestFormatDSMLStream(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:  "Syntax 1 complete tool call",
			input: "<｜DSML｜tool_calls><｜DSML｜invoke name=\"svg_clear\"></｜DSML｜invoke></｜DSML｜tool_calls>",
			expected: "\n\x1b[1;38;5;214m🔧 [Calling Tools]\x1b[m\n" +
				"  👉 \x1b[1;38;5;75mInvoke: svg_clear\x1b[m\n" +
				"\n\x1b[1;38;5;214m🔧 [Tools Completed]\x1b[m\n",
		},
		{
			name:  "Syntax 2 complete tool call",
			input: "<DSML｜tool_calls><DSML｜invoke name=\"svg_clear\"></DSML｜invoke></DSML｜tool_calls>",
			expected: "\n\x1b[1;38;5;214m🔧 [Calling Tools]\x1b[m\n" +
				"  👉 \x1b[1;38;5;75mInvoke: svg_clear\x1b[m\n" +
				"\n\x1b[1;38;5;214m🔧 [Tools Completed]\x1b[m\n",
		},
		{
			name:  "Syntax 3 complete tool call",
			input: "<tool_calls><invoke name=\"svg_clear\"></invoke></tool_calls>",
			expected: "\n\x1b[1;38;5;214m🔧 [Calling Tools]\x1b[m\n" +
				"  👉 \x1b[1;38;5;75mInvoke: svg_clear\x1b[m\n" +
				"\n\x1b[1;38;5;214m🔧 [Tools Completed]\x1b[m\n",
		},
		{
			name:     "Parameter value with HTML entity unescaping",
			input:    "<｜DSML｜parameter name=\"chunk\">&lt;svg width=\"100\"&gt;</｜DSML｜parameter>",
			expected: "    ✏️ \x1b[38;5;244mchunk\x1b[m = \x1b[38;5;86m<svg width=\"100\">\x1b[0m\n",
		},
		{
			name:     "Parameter with string=true attribute",
			input:    "<｜DSML｜parameter name=\"chunk\" string=\"true\">&lt;svg&gt;</｜DSML｜parameter>",
			expected: "    ✏️ \x1b[38;5;244mchunk\x1b[m = \x1b[38;5;86m<svg>\x1b[0m\n",
		},
		{
			name:     "Parameter with single quotes and other attributes",
			input:    "<parameter type='string' name='chunk'>&lt;svg&gt;</parameter>",
			expected: "    ✏️ \x1b[38;5;244mchunk\x1b[m = \x1b[38;5;86m<svg>\x1b[0m\n",
		},
		{
			name:     "Partial tag at the end is hidden",
			input:    "Hello world <｜DSML｜tool",
			expected: "Hello world ",
		},
		{
			name:     "Normal user brackets are preserved",
			input:    "Compare: a < b and y > z",
			expected: "Compare: a < b and y > z",
		},
		{
			name:     "Mixed text and tools",
			input:    "Here is the text.\n<｜DSML｜tool_calls>",
			expected: "Here is the text.\n\n\x1b[1;38;5;214m🔧 [Calling Tools]\x1b[m\n",
		},
		{
			name:     "Case-insensitive tool call with spaces and single quotes",
			input:    "<|DSInvoke Name  =  'svg_clear'></|DSInvoke>",
			expected: "  👉 \x1b[1;38;5;75mInvoke: svg_clear\x1b[m\n",
		},
		{
			name:     "Case-insensitive parameter with single quotes and extra attributes",
			input:    "<|DSparameter type='string' Name='chunk'>&lt;svg&gt;</|DSparameter>",
			expected: "    ✏️ \x1b[38;5;244mchunk\x1b[m = \x1b[38;5;86m<svg>\x1b[0m\n",
		},
		{
			name:     "Partial tag with ASCII vertical bar at end is hidden",
			input:    "Hello world <|DSinvoke",
			expected: "Hello world ",
		},
		{
			name:     "Partial tag with plain XML at end is hidden",
			input:    "Hello world <tool",
			expected: "Hello world ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDSMLStream(tt.input)
			if got != tt.expected {
				t.Errorf("formatDSMLStream(%q)\nGot:      %q\nExpected: %q", tt.input, got, tt.expected)
			}
		})
	}
}
