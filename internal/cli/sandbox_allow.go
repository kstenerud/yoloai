// ABOUTME: `yoloai sandbox <name> allow` handler. Adds network domains
// ABOUTME: to a network-isolated sandbox's allowlist at runtime.
package cli

import (
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func runSandboxAllow(cmd *cobra.Command, name string, domains []string) error {
	if len(domains) == 0 {
		return sandbox.NewUsageError("at least one domain is required")
	}

	sandboxDir, meta, err := loadIsolatedMeta(name)
	if err != nil {
		return err
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
		if jsonEnabled(cmd) {
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"name":          name,
				"domains_added": []string{},
				"live":          false,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "All domains already allowed\n") //nolint:errcheck // best-effort output
		return nil
	}

	// Persist changes
	meta.NetworkAllow = append(meta.NetworkAllow, newDomains...)
	if err := saveNetworkAllowlist(sandboxDir, meta); err != nil {
		return err
	}

	// Try live-patching (only adds new domain IPs to existing ipset)
	backend := resolveBackendForSandbox(name)
	live, patchErr := tryLivePatchNetwork(cmd.Context(), backend, name, ipsetResolveDomains, newDomains)

	w := cmd.OutOrStdout()
	if jsonEnabled(cmd) {
		return writeJSON(w, map[string]any{
			"name":          name,
			"domains_added": newDomains,
			"live":          live,
		})
	}

	switch {
	case live:
		fmt.Fprintf(w, "Allowed %s (live)\n", strings.Join(newDomains, ", ")) //nolint:errcheck // best-effort output
	case patchErr != nil:
		fmt.Fprintf(w, "Warning: failed to update running container: %v\n", patchErr) //nolint:errcheck // best-effort output
		fmt.Fprintf(w, "Changes saved — will take effect on next start\n")            //nolint:errcheck // best-effort output
	default:
		fmt.Fprintf(w, "Allowed %s (will take effect on next start)\n", strings.Join(newDomains, ", ")) //nolint:errcheck // best-effort output
	}

	return nil
}
