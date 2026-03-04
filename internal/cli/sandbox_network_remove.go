// ABOUTME: `yoloai sandbox network remove` subcommand. Removes domains
// ABOUTME: from a network-isolated sandbox's allowlist.
package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newSandboxNetworkRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name> <domain>...",
		Short: "Remove domains from the allowlist",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, domains, err := resolveName(cmd, args)
			if err != nil {
				return err
			}
			if len(domains) == 0 {
				return sandbox.NewUsageError("at least one domain is required")
			}

			backend := resolveBackendForSandbox(name)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				w := cmd.OutOrStdout()

				// Load meta.json
				sandboxDir, err := sandbox.RequireSandboxDir(name)
				if err != nil {
					return err
				}
				meta, err := sandbox.LoadMeta(sandboxDir)
				if err != nil {
					return err
				}

				// Validate network mode
				switch meta.NetworkMode {
				case "isolated":
					// ok
				case "none":
					return fmt.Errorf("sandbox %q uses --network-none; cannot modify network access", name)
				default:
					return fmt.Errorf("sandbox %q is not using network isolation", name)
				}

				// Build set of domains to remove and validate they exist
				toRemove := make(map[string]bool, len(domains))
				existing := make(map[string]bool, len(meta.NetworkAllow))
				for _, d := range meta.NetworkAllow {
					existing[d] = true
				}
				for _, d := range domains {
					if !existing[d] {
						return fmt.Errorf("domain %q is not in the allowlist", d)
					}
					toRemove[d] = true
				}

				// Filter out removed domains
				var remaining []string
				for _, d := range meta.NetworkAllow {
					if !toRemove[d] {
						remaining = append(remaining, d)
					}
				}

				// Update meta.json
				meta.NetworkAllow = remaining
				if err := sandbox.SaveMeta(sandboxDir, meta); err != nil {
					return err
				}

				// Update config.json
				if err := sandbox.PatchConfigAllowedDomains(sandboxDir, meta.NetworkAllow); err != nil {
					return err
				}

				// Check if container is running
				info, err := sandbox.InspectSandbox(ctx, rt, name)
				if err != nil {
					return err
				}

				live := false
				if info.Status == sandbox.StatusRunning {
					// Live-patch: flush ipset and re-add remaining domains
					var scriptParts []string
					scriptParts = append(scriptParts, "ipset flush allowed-domains 2>/dev/null || true")
					if len(remaining) > 0 {
						scriptParts = append(scriptParts, `for domain in "$@"; do
  for ip in $(dig +short A "$domain" 2>/dev/null); do
    echo "$ip" | grep -qE "^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$" && \
      ipset add allowed-domains "$ip" 2>/dev/null || true
  done
done`)
					}
					script := strings.Join(scriptParts, "\n")
					execArgs := []string{"sh", "-c", script, "_"}
					execArgs = append(execArgs, remaining...)
					_, err := rt.Exec(ctx, sandbox.InstanceName(name), execArgs, "0")
					if err != nil {
						if !jsonEnabled(cmd) {
							fmt.Fprintf(w, "Warning: failed to update running container: %v\n", err) //nolint:errcheck // best-effort output
							fmt.Fprintf(w, "Changes saved — will take effect on next start\n")       //nolint:errcheck // best-effort output
						}
					} else {
						live = true
						if !jsonEnabled(cmd) {
							fmt.Fprintf(w, "Removed %s (live)\n", strings.Join(domains, ", ")) //nolint:errcheck // best-effort output
						}
					}
				} else if !jsonEnabled(cmd) {
					fmt.Fprintf(w, "Removed %s (will take effect on next start)\n", strings.Join(domains, ", ")) //nolint:errcheck // best-effort output
				}

				if jsonEnabled(cmd) {
					return writeJSON(cmd.OutOrStdout(), map[string]any{
						"name":            name,
						"domains_removed": domains,
						"live":            live,
					})
				}

				return nil
			})
		},
	}
}
