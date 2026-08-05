// ABOUTME: The log-file constants are spelled guest-relative ("logs/x.jsonl")
// ABOUTME: while host callers use the path helpers; this pins the two together
// ABOUTME: so a tier move cannot silently desync them.
package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every *JSONLFile constant is also handed to the guest and to the Python
// writers, which join it onto their own root. The host-side helper must land on
// the same file, so the helper has to resolve to <logs dir>/<constant's base>.
// Asserting against LogsPath rather than a literal is deliberate: when logs/
// moves into a tier, this test follows it and still catches a helper that did
// not.
func TestLogPathHelpers_ResolveUnderLogsDir(t *testing.T) {
	const dir = "/sandboxes/demo"

	for _, tc := range []struct {
		name     string
		constant string
		path     string
	}{
		{"cli", CLIJSONLFile, CLIJSONLPath(dir)},
		{"sandbox", SandboxJSONLFile, SandboxJSONLPath(dir)},
		{"monitor", MonitorJSONLFile, MonitorJSONLPath(dir)},
		{"hooks", HooksJSONLFile, HooksJSONLPath(dir)},
		{"agent", AgentLogFile, AgentLogPath(dir)},
		{"secrets-consumed", SecretsConsumedMarker, SecretsConsumedMarkerPath(dir)},
		{"substrate-ready", SubstrateReadyMarker, SubstrateReadyMarkerPath(dir)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, filepath.Join(LogsPath(dir), filepath.Base(tc.constant)), tc.path,
				"the guest joins the constant onto its own root; the helper must reach the same file")
		})
	}
}

// The set create pre-creates and reset re-creates must stay the guest-written
// logs, in the logs dir. A helper that drifted out of the dir would leave the
// guest appending to a file nothing on the host reads.
func TestGuestLogFilePaths_AreAllUnderLogsDir(t *testing.T) {
	const dir = "/sandboxes/demo"

	paths := GuestLogFilePaths(dir)
	require.Len(t, paths, 3)
	for _, p := range paths {
		assert.Equal(t, LogsPath(dir), filepath.Dir(p), "%s must sit in the logs dir", p)
	}
	assert.Contains(t, paths, SandboxJSONLPath(dir))
	assert.Contains(t, paths, MonitorJSONLPath(dir))
	assert.Contains(t, paths, HooksJSONLPath(dir))
}
