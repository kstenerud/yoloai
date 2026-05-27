package sandboxcmd

// ABOUTME: Sandbox listing logic shared by `yoloai sandbox list` and the
// ABOUTME: top-level `yoloai ls` shortcut.

import (
	"fmt"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// listFilters holds the filter criteria for the list command.
type listFilters struct {
	active  bool
	idle    bool
	done    bool
	stopped bool
	agent   string
	profile string
	changes bool
}

func newSandboxListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sandboxes and their status",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
	addListFlags(cmd)
	return cmd
}

// addListFlags adds filter flags shared by `sandbox list` and the `ls` alias.
func addListFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("active", false, "Show only active sandboxes (includes idle)")
	cmd.Flags().Bool("idle", false, "Show only idle sandboxes")
	cmd.Flags().Bool("done", false, "Show only done or failed sandboxes")
	cmd.Flags().Bool("stopped", false, "Show only stopped sandboxes")
	cmd.Flags().String("agent", "", "Show only sandboxes using this agent")
	cmd.Flags().String("profile", "", "Show only sandboxes using this profile")
	cmd.Flags().Bool("changes", false, "Show only sandboxes with unapplied changes")
}

// filterInfos applies the given filters to a slice of sandbox infos.
// Multiple filters are ANDed together. Broken sandboxes are excluded by
// all filters except when no filters are active.
func filterInfos(infos []*sandbox.Info, f listFilters) []*sandbox.Info {
	if !f.active && !f.idle && !f.done && !f.stopped && f.agent == "" && f.profile == "" && !f.changes {
		return infos
	}

	var result []*sandbox.Info
	for _, info := range infos {
		if matchesFilters(info, f) {
			result = append(result, info)
		}
	}
	return result
}

// matchesFilters returns true if info satisfies all active filter criteria.
// matchesStatusFilter returns false if the sandbox status does not satisfy
// the status-related flags (active, idle, done, stopped).
func matchesStatusFilter(info *sandbox.Info, f listFilters) bool {
	if f.active && info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
		return false
	}
	if f.idle && info.Status != sandbox.StatusIdle {
		return false
	}
	if f.done && info.Status != sandbox.StatusDone && info.Status != sandbox.StatusFailed {
		return false
	}
	if f.stopped && info.Status != sandbox.StatusStopped && info.Status != sandbox.StatusSuspended {
		return false
	}
	return true
}

func matchesFilters(info *sandbox.Info, f listFilters) bool {
	if !matchesStatusFilter(info, f) {
		return false
	}
	if f.agent != "" {
		if info.Status == sandbox.StatusBroken || info.Meta.Agent != f.agent {
			return false
		}
	}
	if f.profile != "" && !matchesProfileFilter(info, f.profile) {
		return false
	}
	if f.changes && info.HasChanges != "yes" {
		return false
	}
	return true
}

// matchesProfileFilter returns true if the sandbox matches the profile filter.
func matchesProfileFilter(info *sandbox.Info, profileFilter string) bool {
	if info.Status == sandbox.StatusBroken {
		return false
	}
	p := info.Meta.Profile
	if profileFilter == "base" {
		return p == "" || p == "base"
	}
	return p == profileFilter
}

// formatProfile returns the display string for a profile name.
// Empty profile (the default) is shown as "(base)".
func formatProfile(profile string) string {
	if profile == "" {
		return "(base)"
	}
	return profile
}

// runList is the shared implementation for `sandbox list` and the `ls` alias.
func runList(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	// Use multi-backend listing
	infos, unavailableBackends, err := sandbox.ListSandboxesMultiBackend(ctx, cliutil.Layout(), cliutil.NewRuntime)
	if err != nil {
		return err
	}

	// Read filter flags.
	active, _ := cmd.Flags().GetBool("active")
	idle, _ := cmd.Flags().GetBool("idle")
	done, _ := cmd.Flags().GetBool("done")
	stopped, _ := cmd.Flags().GetBool("stopped")
	agent, _ := cmd.Flags().GetString("agent")
	profile, _ := cmd.Flags().GetString("profile")
	changes, _ := cmd.Flags().GetBool("changes")

	infos = filterInfos(infos, listFilters{
		active:  active,
		idle:    idle,
		done:    done,
		stopped: stopped,
		agent:   agent,
		profile: profile,
		changes: changes,
	})

	if cliutil.JSONEnabled(cmd) {
		if infos == nil {
			infos = []*sandbox.Info{}
		}
		// Create output structure with unavailable_backends field
		output := map[string]any{
			"sandboxes":            infos,
			"unavailable_backends": unavailableBackends,
		}
		if unavailableBackends == nil {
			output["unavailable_backends"] = []string{}
		}
		return cliutil.WriteJSON(cmd.OutOrStdout(), output)
	}

	if len(infos) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No sandboxes found") //nolint:errcheck
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tBACKEND\tAGENT\tPROFILE\tAGE\tSIZE\tWORKDIR\tCHANGES") //nolint:errcheck
	for _, info := range infos {
		if info.Status == sandbox.StatusBroken || info.Status == sandbox.StatusUnavailable {
			backend := "-"
			if info.Meta.Backend != "" {
				backend = info.Meta.Backend
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck
				info.Meta.Name,
				info.Status,
				backend,
				"-",
				"-",
				"-",
				info.DiskUsage,
				"-",
				"-",
			)
			continue
		}
		backend := info.Meta.Backend
		if backend == "" {
			backend = "docker" // fallback for old sandboxes without backend field
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck
			info.Meta.Name,
			info.Status,
			backend,
			info.Meta.Agent,
			formatProfile(info.Meta.Profile),
			sandbox.FormatAge(info.Meta.CreatedAt),
			info.DiskUsage,
			info.Meta.Workdir.HostPath,
			info.HasChanges,
		)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Display footer note if any backends are unavailable
	if len(unavailableBackends) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "")                                                                                           //nolint:errcheck
		fmt.Fprintf(cmd.OutOrStdout(), "Note: The following backends are unavailable: %s\n", strings.Join(unavailableBackends, ", ")) //nolint:errcheck
		fmt.Fprintln(cmd.OutOrStdout(), "Sandboxes using these backends show status 'unavailable'.")                                  //nolint:errcheck
	}

	slog.Debug("list complete", "event", "sandbox.list", "count", len(infos))
	return nil
}
