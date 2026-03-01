package cli

// ABOUTME: `yoloai sandbox network-allow` subcommand. Adds network domains
// ABOUTME: to a running network-isolated sandbox at runtime.

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSandboxNetworkAllowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "network-allow <name> <domain>...",
		Short: "Allow additional domains in an isolated sandbox",
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
					return fmt.Errorf("sandbox %q uses --network-none; cannot add network access", name)
				default:
					return fmt.Errorf("sandbox %q is not using network isolation", name)
				}

				// Deduplicate new domains against existing allowlist
				existing := make(map[string]bool, len(meta.NetworkAllow))
				for _, d := range meta.NetworkAllow {
					existing[d] = true
				}
				var newDomains []string
				for _, d := range domains {
					if !existing[d] {
						newDomains = append(newDomains, d)
						existing[d] = true // prevent duplicates within the input
					}
				}

				if len(newDomains) == 0 {
					fmt.Fprintf(w, "All domains already allowed\n") //nolint:errcheck // best-effort output
					return nil
				}

				// Update meta.json
				meta.NetworkAllow = append(meta.NetworkAllow, newDomains...)
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

				if info.Status == sandbox.StatusRunning {
					// Live-patch ipset rules in running container
					script := `for domain in "$@"; do
  for ip in $(dig +short A "$domain" 2>/dev/null); do
    echo "$ip" | grep -qE "^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$" && \
      ipset add allowed-domains "$ip" 2>/dev/null || true
  done
done`
					execArgs := []string{"sh", "-c", script, "_"}
					execArgs = append(execArgs, newDomains...)
					_, err := rt.Exec(ctx, sandbox.InstanceName(name), execArgs, "0")
					if err != nil {
						fmt.Fprintf(w, "Warning: failed to update running container: %v\n", err) //nolint:errcheck // best-effort output
						fmt.Fprintf(w, "Changes saved — will take effect on next start\n")       //nolint:errcheck // best-effort output
					} else {
						fmt.Fprintf(w, "Allowed %s (live)\n", strings.Join(newDomains, ", ")) //nolint:errcheck // best-effort output
					}
				} else {
					fmt.Fprintf(w, "Allowed %s (will take effect on next start)\n", strings.Join(newDomains, ", ")) //nolint:errcheck // best-effort output
				}

				return nil
			})
		},
	}
}
