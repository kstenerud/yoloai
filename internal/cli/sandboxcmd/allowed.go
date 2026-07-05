// ABOUTME: `yoloai sandbox <name> allowed` handler. Shows allowed domains
// ABOUTME: for a network-isolated sandbox with their provenance.
package sandboxcmd

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/spf13/cobra"
)

// runSandboxAllowed prints a sandbox's allowlist with each entry's
// provenance source (agent-requirement vs user-added). The library's
// Network.Allowed() does the derivation; this just renders.
func runSandboxAllowed(cmd *cobra.Command, name string) error {
	return cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		// Branch on NetworkMode early so the "not isolated" / "none"
		// cases render their static messages without making the library
		// load the allowlist. Network.Allowed() doesn't reject those
		// states (read-only never errors) — we surface them here.
		// Read the configured mode from netpolicy.json (D90) rather than a
		// full Inspect, so listing the allowlist needs no running backend.
		networkMode, err := sb.Network().Mode()
		if err != nil {
			return err
		}

		allowed, err := sb.Network().Allowed(ctx)
		if err != nil {
			return err
		}

		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
				"name":         name,
				"network_mode": networkMode,
				"domains":      cliutil.EmptyIfNil(allowed),
			})
		}

		w := cmd.OutOrStdout()
		switch networkMode {
		case "none":
			fmt.Fprintln(w, "Network disabled (--network-none)") //nolint:errcheck
		case "isolated":
			if len(allowed) == 0 {
				fmt.Fprintln(w, "No domains allowed") //nolint:errcheck
				return nil
			}
			for _, d := range allowed {
				marker := ""
				if d.Source == yoloai.AllowedFromAgentRequirement {
					marker = " (agent requirement)"
				}
				fmt.Fprintf(w, "%s%s\n", d.Domain, marker) //nolint:errcheck
			}
		default:
			fmt.Fprintln(w, "No network isolation") //nolint:errcheck
		}
		return nil
	})
}
