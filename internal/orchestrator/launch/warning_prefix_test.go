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
	// Loopback rather than all interfaces (gosec G102): the helper binds
	// 0.0.0.0, which still collides with a loopback holder on the same port, so
	// the collision under test is unaffected — and the assertion that the port
	// was dropped is what would catch it if that ever stopped being true.
	held, err := net.Listen("tcp", "127.0.0.1:0")
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
