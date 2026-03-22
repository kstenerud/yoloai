// ABOUTME: `yoloai sandbox <name> deny` handler. Removes domains
// ABOUTME: from a network-isolated sandbox's allowlist.
package cli

import (
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func runSandboxDeny(cmd *cobra.Command, name string, domains []string) error {
	if len(domains) == 0 {
		return sandbox.NewUsageError("at least one domain is required")
	}

	sandboxDir, meta, err := loadIsolatedMeta(name)
	if err != nil {
		return err
	}

	// Validate all domains exist in allowlist
	existing := make(map[string]bool, len(meta.NetworkAllow))
	for _, d := range meta.NetworkAllow {
		existing[d] = true
	}
	toRemove := make(map[string]bool, len(domains))
	for _, d := range domains {
		if !existing[d] {
			return sandbox.NewUsageError("domain %q is not in the allowlist", d)
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

	// Persist changes
	meta.NetworkAllow = remaining
	if err := saveNetworkAllowlist(sandboxDir, meta); err != nil {
		return err
	}

	// Try live-patching (flush ipset and re-add remaining domain IPs)
	script := "ipset flush allowed-domains 2>/dev/null || true"
	if len(remaining) > 0 {
		script += "\n" + ipsetResolveDomains
	}
	backend := resolveBackendForSandbox(name)
	live, patchErr := tryLivePatchNetwork(cmd.Context(), backend, name, script, remaining)

	w := cmd.OutOrStdout()
	if jsonEnabled(cmd) {
		return writeJSON(w, map[string]any{
			"name":            name,
			"domains_removed": domains,
			"live":            live,
		})
	}

	switch {
	case live:
		fmt.Fprintf(w, "Removed %s (live)\n", strings.Join(domains, ", ")) //nolint:errcheck // best-effort output
	case patchErr != nil:
		fmt.Fprintf(w, "Warning: failed to update running container: %v\n", patchErr) //nolint:errcheck // best-effort output
		fmt.Fprintf(w, "Changes saved — will take effect on next start\n")            //nolint:errcheck // best-effort output
	default:
		fmt.Fprintf(w, "Removed %s (will take effect on next start)\n", strings.Join(domains, ", ")) //nolint:errcheck // best-effort output
	}

	return nil
}
