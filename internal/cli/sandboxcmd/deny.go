// ABOUTME: `yoloai sandbox <name> deny` handler. Removes domains
// ABOUTME: from a network-isolated sandbox's allowlist.
package sandboxcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
)

func runSandboxDeny(cmd *cobra.Command, name string, domains []string) error {
	return cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		result, err := sb.Network().Deny(ctx, domains...)
		if err != nil {
			return err
		}

		w := cmd.OutOrStdout()
		removedDomains := removedDomainStrings(result)

		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(w, map[string]any{
				"name":            name,
				"domains_removed": result.Removed, // typed; carries source
				"live":            result.Live,
			})
		}

		if hint := agentRequirementHint(result); hint != "" {
			fmt.Fprintln(w, hint) //nolint:errcheck
		}

		switch {
		case result.Live:
			fmt.Fprintf(w, "Removed %s (live)\n", strings.Join(removedDomains, ", ")) //nolint:errcheck
		default:
			fmt.Fprintf(w, "Removed %s (will take effect on next start)\n", strings.Join(removedDomains, ", ")) //nolint:errcheck
		}
		return nil
	})
}

// removedDomainStrings projects DenyResult.Removed back to a flat
// []string for human-readable output.
func removedDomainStrings(result *yoloai.DenyResult) []string {
	out := make([]string, 0, len(result.Removed))
	for _, d := range result.Removed {
		out = append(out, d.Domain)
	}
	return out
}

// agentRequirementHint returns the warning text printed when any of
// the just-removed domains was an agent requirement, surfacing
// Q-V's provenance to the user — removing api.anthropic.com from a
// Claude sandbox isn't a typo, but it'll break the agent. Empty
// string when nothing agent-required was removed.
func agentRequirementHint(result *yoloai.DenyResult) string {
	var hits []string
	for _, d := range result.Removed {
		if d.Source == yoloai.AllowedFromAgentRequirement {
			hits = append(hits, d.Domain)
		}
	}
	if len(hits) == 0 {
		return ""
	}
	return fmt.Sprintf("Note: removed agent-required domain(s): %s — the agent may stop working.", strings.Join(hits, ", "))
}
