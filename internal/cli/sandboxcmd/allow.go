// ABOUTME: `yoloai sandbox <name> allow` handler. Adds network domains
// ABOUTME: to a network-isolated sandbox's allowlist at runtime.
package sandboxcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
)

func runSandboxAllow(cmd *cobra.Command, name string, domains []string) error {
	return cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		result, err := sb.Network().Allow(ctx, domains...)
		if err != nil {
			return err
		}

		w := cmd.OutOrStdout()
		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(w, map[string]any{
				"name":          name,
				"domains_added": result.Added,
				"live":          result.Live,
			})
		}

		if len(result.Added) == 0 {
			fmt.Fprintf(w, "All domains already allowed\n") //nolint:errcheck // best-effort output
			return nil
		}

		switch {
		case result.Live:
			fmt.Fprintf(w, "Allowed %s (live)\n", strings.Join(result.Added, ", ")) //nolint:errcheck
		default:
			fmt.Fprintf(w, "Allowed %s (will take effect on next start)\n", strings.Join(result.Added, ", ")) //nolint:errcheck
		}
		return nil
	})
}
