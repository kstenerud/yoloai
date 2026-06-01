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
	// Branch on NetworkMode early so the "not isolated" / "none"
	// cases render their static messages without making the library
	// load the allowlist. Network.Allowed() doesn't reject those
	// states (read-only never errors) — we surface them here.
	meta, err := loadEnvironmentForRead(name)
	if err != nil {
		return err
	}

	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, err := c.Sandbox(name)
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
				"network_mode": meta.NetworkMode,
				"domains":      allowed,
			})
		}

		w := cmd.OutOrStdout()
		switch meta.NetworkMode {
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

// loadEnvironmentForRead reads the sandbox's metadata without enforcing the
// "isolated mode required" precondition. The `allowed` subcommand needs to
// print specific messages for the other network modes, so it can't go through
// requireIsolated.
func loadEnvironmentForRead(name string) (*yoloai.Environment, error) {
	return cliutil.NewSystemClient().SandboxMetadata(name)
}
