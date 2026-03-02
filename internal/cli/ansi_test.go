package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text passthrough",
			input: "hello world\n",
			want:  "hello world\n",
		},
		{
			name:  "SGR color codes",
			input: "\x1b[31mred\x1b[0m normal\n",
			want:  "red normal\n",
		},
		{
			name:  "cursor movement",
			input: "\x1b[2J\x1b[H$ prompt\n",
			want:  "$ prompt\n",
		},
		{
			name:  "OSC title sequence",
			input: "\x1b]0;my title\x07some text\n",
			want:  "some text\n",
		},
		{
			name:  "character set selection",
			input: "\x1b(Bhello\x1b)0world\n",
			want:  "helloworld\n",
		},
		{
			name:  "mixed sequences",
			input: "\x1b[1;32m$ \x1b[0mecho \x1b]0;bash\x07hello\n",
			want:  "$ echo hello\n",
		},
		{
			name:  "multiple lines",
			input: "\x1b[31mline1\x1b[0m\nline2\n\x1b[32mline3\x1b[0m\n",
			want:  "line1\nline2\nline3\n",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "CSI with question mark",
			input: "\x1b[?25hvisible\x1b[?25l\n",
			want:  "visible\n",
		},
		{
			name:  "null and bell characters",
			input: "hel\x00lo\x07world\n",
			want:  "helloworld\n",
		},
		{
			name:  "carriage return stripped",
			input: "progress: 100%\roverwritten\n",
			want:  "progress: 100%overwritten\n",
		},
		{
			name:  "backspace and DEL",
			input: "ab\x08c\x7fd\n",
			want:  "abcd\n",
		},
		{
			name:  "tab preserved",
			input: "col1\tcol2\tcol3\n",
			want:  "col1\tcol2\tcol3\n",
		},
		{
			name:  "vertical tab and form feed stripped",
			input: "before\x0bmid\x0cafter\n",
			want:  "beforemidafter\n",
		},
		{
			name:  "mixed ANSI and control chars",
			input: "\x1b[31m\x00red\x08\x1b[0m\n",
			want:  "red\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := stripANSI(&buf, strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("stripANSI() error = %v", err)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("stripANSI() = %q, want %q", got, tt.want)
			}
		})
	}
}
