// ABOUTME: `yoloai sandbox <name> unlock` — manually clear a stale per-
// ABOUTME: sandbox lock file. Refuses if the recorded holder PID is alive.
package sandboxcmd

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
)

// runSandboxUnlock force-clears a stale lock file for the named sandbox.
// Surfaces Sandbox.Unlock's *UsageError verbatim when the holder
// is alive. Distinguishes "cleared a stale lock" from "no lock file
// present" so the user gets an honest report — relevant when the
// command is run defensively (in a recovery script, etc.) and there
// was nothing actually stale.
func runSandboxUnlock(cmd *cobra.Command, name string) error {
	c, err := cliutil.Client(cmd)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup
	sb, err := c.Sandbox(name)
	if err != nil {
		return err
	}
	cleared, err := sb.Unlock()
	if err != nil {
		return err
	}
	action := "cleared"
	if !cleared {
		action = "noop"
	}
	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{
			"name":   name,
			"action": action,
		})
	}
	msg := fmt.Sprintf("Cleared stale lock for sandbox %q\n", name)
	if !cleared {
		msg = fmt.Sprintf("No lock file present for sandbox %q\n", name)
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), msg)
	return err
}
