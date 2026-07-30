// ABOUTME: Guards the WarningPrefix convention: a launch-path helper writing a
// ABOUTME: warning to State.Output must mark it, or the restart path reads it as
// ABOUTME: progress and it is rendered to stdout instead of stderr (DF157).

package launch

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFilterAvailablePorts_MarksItsWarning holds the producer end of the
// contract DF157's fix depends on. State.Output is a mixed stream, so on the
// restart path a noticeWriter decides each line's level from WarningPrefix
// alone. An unmarked warning is therefore not merely styled differently — it
// becomes a NoticeInfo, which goes to stdout and is suppressed under --json.
//
// Asserting on the prefix constant rather than on the word "Warning" is the
// point: it is the same symbol the classifier keys on, so the two cannot drift.
func TestFilterAvailablePorts_MarksItsWarning(t *testing.T) {
	// Hold a real port for the duration, so the helper's own net.Listen fails
	// for the reason it exists to detect rather than for a simulated one.
	//
	// The holder MUST bind the same way filterAvailablePorts does — all
	// interfaces, not loopback. Holding 127.0.0.1:P and expecting a 0.0.0.0:P
	// bind to collide is Linux-specific: on macOS/BSD that second bind succeeds,
	// so the port is never reported busy and this test silently stops testing
	// anything. Observed as a real failure on darwin, where the helper bound a
	// port a loopback listener already held.
	held, err := net.Listen("tcp", ":0") //nolint:gosec // G102: must mirror filterAvailablePorts' own bind; a loopback holder does not collide on BSD
	require.NoError(t, err)
	defer held.Close() //nolint:errcheck // test cleanup
	busy := held.Addr().(*net.TCPAddr).Port

	var out bytes.Buffer
	kept := filterAvailablePorts([]runtime.PortMapping{{HostPort: busy, ContainerPort: 80}}, &out)

	assert.Empty(t, kept, "a host port already in use must be dropped, not handed to the backend")
	assert.True(t, strings.HasPrefix(out.String(), WarningPrefix),
		"the warning must carry WarningPrefix: without it the restart path files it as progress\ngot: %q", out.String())
	assert.Contains(t, out.String(), fmt.Sprintf("%d", busy))
}
