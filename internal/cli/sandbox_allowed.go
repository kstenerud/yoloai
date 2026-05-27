// ABOUTME: `yoloai sandbox <name> allowed` handler. Shows allowed domains
// ABOUTME: for a network-isolated sandbox.
package cli

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/spf13/cobra"
)

func runSandboxAllowed(cmd *cobra.Command, name string) error {
	sandboxDir := cliutil.Layout().SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return err
	}
	meta, err := store.LoadMeta(sandboxDir)
	if err != nil {
		return err
	}

	if cliutil.JSONEnabled(cmd) {
		domains := meta.NetworkAllow
		if domains == nil {
			domains = []string{}
		}
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
			"name":         name,
			"network_mode": meta.NetworkMode,
			"domains":      domains,
		})
	}

	w := cmd.OutOrStdout()
	switch meta.NetworkMode {
	case "none":
		fmt.Fprintln(w, "Network disabled (--network-none)") //nolint:errcheck // best-effort output
	case "isolated":
		if len(meta.NetworkAllow) == 0 {
			fmt.Fprintln(w, "No domains allowed") //nolint:errcheck // best-effort output
		} else {
			for _, d := range meta.NetworkAllow {
				fmt.Fprintln(w, d) //nolint:errcheck // best-effort output
			}
		}
	default:
		fmt.Fprintln(w, "No network isolation") //nolint:errcheck // best-effort output
	}
	return nil
}
