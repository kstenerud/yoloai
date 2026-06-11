// ABOUTME: Unit tests for PTYBridgeExec — the local-PTY bridge shared by the
// ABOUTME: seatbelt/tart/apple backends. Runs real host commands under a PTY.

package runtime

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/sysexec"
)

// A TTY exec with a nil Out must not panic: PTYBridgeExec drains the PTY master
// to io.Discard rather than dereferencing a nil writer. Exit codes still surface.
func TestPTYBridgeExec_TTYNilOutDoesNotPanic(t *testing.T) {
	err := PTYBridgeExec(sysexec.Command([]string{}, "sh", "-c", "printf hi; exit 9"), IOStreams{TTY: true})
	var execErr *ExecError
	require.ErrorAs(t, err, &execErr, "non-zero exit must surface as *ExecError even with nil Out")
	assert.Equal(t, 9, execErr.ExitCode)
}

// With a real Out the child's PTY output is copied through verbatim.
func TestPTYBridgeExec_TTYCapturesOutput(t *testing.T) {
	var out strings.Builder
	err := PTYBridgeExec(sysexec.Command([]string{}, "sh", "-c", "printf hello"), IOStreams{Out: &out, TTY: true})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "hello")
}

// The non-TTY path tolerates nil streams too (exec maps nil Stdout to /dev/null).
func TestPTYBridgeExec_NonTTYNilStreams(t *testing.T) {
	err := PTYBridgeExec(sysexec.Command([]string{}, "true"), IOStreams{})
	assert.NoError(t, err)

	err = PTYBridgeExec(sysexec.Command([]string{}, "sh", "-c", "exit 3"), IOStreams{})
	var execErr *ExecError
	require.ErrorAs(t, err, &execErr)
	assert.Equal(t, 3, execErr.ExitCode)
}
