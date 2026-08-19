// ABOUTME: The brokering posture is settable on every command that launches a
// ABOUTME: sandbox, not only on `new` — otherwise changing it means recreating.
package lifecycle

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The posture is sticky and persisted, so a sandbox created one way could only
// be moved the other way by recreating it — which destroys the sandbox to change
// a launch option. `start` and `restart` reach the same applyBrokerOption path
// `new` does, so the flags belong on all three (DF223 made this routine: the
// workaround for a degraded brokered sandbox is to turn brokering off).
func TestBrokerFlags_OnEveryLaunchingCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
	}{
		{"new", NewNewCmd("test")},
		{"start", NewStartCmd()},
		{"restart", NewRestartCmd()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NotNil(t, tc.cmd.Flags().Lookup("broker"), "--broker must be settable on %q", tc.name)
			require.NotNil(t, tc.cmd.Flags().Lookup("no-broker"), "--no-broker must be settable on %q", tc.name)

			// Both at once is meaningless; cobra must reject it rather than let
			// one silently win. (The underlying tri-state is encoded as two
			// booleans — DF225.)
			require.NoError(t, tc.cmd.Flags().Set("broker", "true"))
			require.NoError(t, tc.cmd.Flags().Set("no-broker", "true"))
			err := tc.cmd.ValidateFlagGroups()
			assert.Error(t, err, "--broker and --no-broker must be mutually exclusive on %q", tc.name)
		})
	}
}
