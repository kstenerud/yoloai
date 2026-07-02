// ABOUTME: Unit tests for ptybridge.Exec — the local-PTY bridge shared by the
// ABOUTME: seatbelt/tart/apple backends. Runs real host commands under a PTY.

package ptybridge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
)

// A TTY exec with a nil Out must not panic: Exec drains the PTY master to
// io.Discard rather than dereferencing a nil writer. Exit codes still surface.
func TestExec_TTYNilOutDoesNotPanic(t *testing.T) {
	err := Exec(sysexec.Command([]string{}, "sh", "-c", "printf hi; exit 9"), runtime.IOStreams{TTY: true})
	var execErr *runtime.ExecError
	require.ErrorAs(t, err, &execErr, "non-zero exit must surface as *ExecError even with nil Out")
	assert.Equal(t, 9, execErr.ExitCode)
}

// With a real Out the child's PTY output is copied through verbatim.
func TestExec_TTYCapturesOutput(t *testing.T) {
	var out strings.Builder
	err := Exec(sysexec.Command([]string{}, "sh", "-c", "printf hello"), runtime.IOStreams{Out: &out, TTY: true})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "hello")
}

// The non-TTY path tolerates nil streams too (exec maps nil Stdout to /dev/null).
func TestExec_NonTTYNilStreams(t *testing.T) {
	err := Exec(sysexec.Command([]string{}, "true"), runtime.IOStreams{})
	assert.NoError(t, err)

	err = Exec(sysexec.Command([]string{}, "sh", "-c", "exit 3"), runtime.IOStreams{})
	var execErr *runtime.ExecError
	require.ErrorAs(t, err, &execErr)
	assert.Equal(t, 3, execErr.ExitCode)
}

// crStripper undoes the remote-PTY exec's ONLCR: it removes exactly one CR before
// each LF, so a cud1 (\r\n after ONLCR) becomes \n and a nel (\r\r\n after ONLCR)
// becomes \r\n, while a lone CR (column-0 return) is preserved. The transform must
// hold across arbitrary chunk boundaries, since the PTY master is read in chunks.
func TestCRStripper(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"cud1 restored", []string{"a\r\nb"}, "a\nb"},
		{"nel restored", []string{"a\r\r\nb"}, "a\r\nb"},
		{"lone CR preserved", []string{"a\rb"}, "a\rb"},
		{"trailing lone CR flushed", []string{"a\r"}, "a\r"},
		{"CR split before LF", []string{"a\r", "\nb"}, "a\nb"},
		{"CR split before non-LF", []string{"a\r", "xb"}, "a\rxb"},
		{"double CR split", []string{"a\r\r", "\nb"}, "a\r\nb"},
		{"empty then LF", []string{"\r", "\n"}, "\n"},
		{"mixed sequence", []string{"x\r\r\ny\r\nz\rw"}, "x\r\ny\nz\rw"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			s := &crStripper{w: &out}
			for _, c := range tc.chunks {
				n, err := s.Write([]byte(c))
				require.NoError(t, err)
				assert.Equal(t, len(c), n, "Write must report full consumption")
			}
			require.NoError(t, s.flush())
			assert.Equal(t, tc.want, out.String())
		})
	}
}
