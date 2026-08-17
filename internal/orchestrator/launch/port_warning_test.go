// ABOUTME: Guards the level of the launch path's port warning: as info it goes
// ABOUTME: to stdout and is suppressed under --json, so a dropped port would be
// ABOUTME: invisible in exactly the mode a script reads (DF157).

package launch

import (
	"net"
	"testing"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFilterAvailablePorts_MarksItsWarning holds what DF157's fix was actually
// about. A dropped port reported at info level goes to stdout and is suppressed
// under --json — silently, in the mode a script reads — so the level is the
// behaviour, not the styling.
//
// It used to assert that the message carried WarningPrefix, because the level
// was recovered from the text by a classifier further down. Now the level is a
// field on the record and the assertion says so directly; there is no longer a
// string for a producer and a classifier to drift apart on (D145).
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

	var got feedback.Collector
	kept := filterAvailablePorts([]runtime.PortMapping{{HostPort: busy, ContainerPort: 80}}, &got)

	assert.Empty(t, kept, "a host port already in use must be dropped, not handed to the backend")
	notices := got.Notices()
	require.Len(t, notices, 1, "dropping a port silently is the failure mode")
	assert.Equal(t, feedback.LevelWarn, notices[0].Level,
		"at info level this goes to stdout and vanishes under --json (DF157)")
	assert.Equal(t, "ports.unavailable", notices[0].Event)
	assert.Equal(t, busy, notices[0].Fields["host_port"],
		"which port was dropped must be readable as data, not parsed out of the message")
}
