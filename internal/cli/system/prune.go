package system

// ABOUTME: `yoloai system prune` removes orphaned backend resources and stale temp files.

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSystemPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove orphaned backend resources and stale temp files",
		Long: `Remove orphaned backend resources and stale temp files.

Always operates across every backend that's currently available. Removes
only orphans (sandbox-named containers/VMs with no matching sandbox dir
on the host) and stale yoloai temp dirs — safe to run anytime.

--cache also reclaims each backend's image cache, snapshots, volumes,
and build cache (forces yoloai-base to rebuild on next sandbox creation).
Use on a host dedicated to yoloai; on shared machines, prefer the
backend's own prune (e.g., 'docker system prune').`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			yes := cliutil.EffectiveYes(cmd)
			cache, _ := cmd.Flags().GetBool("cache")
			return runSystemPrune(cmd, dryRun, yes, cache)
		},
	}

	cmd.Flags().Bool("dry-run", false, "Report only, don't remove anything")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().Bool("cache", false, "Also reclaim backend image cache + snapshots + build cache (DESTRUCTIVE: forces base rebuild)")

	return cmd
}

func runSystemPrune(cmd *cobra.Command, dryRun, yes, cache bool) error {
	ctx := cmd.Context()
	output := cmd.OutOrStdout()
	isJSON := cliutil.JSONEnabled(cmd)

	// First, a dry-run scan to find what's there.
	scanResult, err := cliutil.NewSystemClient().Prune(ctx, yoloai.PruneOptions{
		DryRun:           true,
		IncludeBaseImage: cache,
		Output:           output,
	})
	if err != nil {
		return err
	}

	printBrokenSandboxWarnings(output, scanResult.BrokenSandboxes, isJSON)

	totalItems := len(scanResult.RemovedItems)
	if done := reportPruneEarly(cmd, output, scanResult, totalItems, dryRun, cache, isJSON); done {
		return nil
	}
	announceCache(output, cache, isJSON)

	if dryRun {
		if isJSON {
			return writePruneJSON(cmd, scanResult, true)
		}
		return nil
	}
	if !yes {
		confirmed, err := confirmPrune(cmd, ctx, totalItems, cache)
		if err != nil || !confirmed {
			return err
		}
	}

	// Actual removal. The library does the work; we just report.
	actualResult, err := cliutil.NewSystemClient().Prune(ctx, yoloai.PruneOptions{
		DryRun:           false,
		IncludeBaseImage: cache,
		Output:           output,
	})
	if err != nil {
		return err
	}
	if !isJSON {
		for _, item := range actualResult.RemovedItems {
			label := item.Kind
			if label == "temp_dir" {
				fmt.Fprintf(output, "Removed temp dir %s\n", item.Name) //nolint:errcheck
			} else {
				fmt.Fprintf(output, "Removed %s %s\n", label, item.Name) //nolint:errcheck
			}
		}
	}
	if isJSON {
		return writePruneJSON(cmd, actualResult, false)
	}
	return nil
}

// reportPruneEarly handles the "nothing to do" exit and the
// orphan-list print. Returns true if the caller should return
// immediately (nothing left to do).
func reportPruneEarly(cmd *cobra.Command, output interface{ Write([]byte) (int, error) }, result *yoloai.PruneResult, totalItems int, dryRun, cache, isJSON bool) bool {
	if totalItems == 0 && !cache {
		if isJSON {
			_ = writePruneJSON(cmd, result, dryRun)
		} else {
			fmt.Fprintln(output, "Nothing to prune.")                                                           //nolint:errcheck
			fmt.Fprintln(output, "(For stuck containers, leftover netns, or stale state: yoloai help cleanup)") //nolint:errcheck
		}
		return true
	}
	if totalItems > 0 {
		printPruneFoundItems(output, result.RemovedItems, isJSON)
	}
	return false
}

// announceCache prints the cache-reclaim banner (human-mode only).
func announceCache(output interface{ Write([]byte) (int, error) }, cache, isJSON bool) {
	if !cache || isJSON {
		return
	}
	fmt.Fprintln(output, "Cache reclaim requested across all available backends.") //nolint:errcheck
	fmt.Fprintln(output, "  (forces base image rebuild on next sandbox creation)") //nolint:errcheck
}

// confirmPrune prompts the user to confirm removal and returns
// whether they confirmed.
func confirmPrune(cmd *cobra.Command, ctx context.Context, totalItems int, cache bool) (bool, error) {
	var prompt string
	switch {
	case totalItems == 0 && cache:
		prompt = "Reclaim backend cache (rebuilds yoloai-base on next 'new')? [y/N]: "
	case cache:
		prompt = fmt.Sprintf("Remove %d resource(s) + reclaim backend cache (rebuilds yoloai-base on next 'new')? [y/N]: ", totalItems)
	default:
		prompt = fmt.Sprintf("Remove %d resource(s)? [y/N]: ", totalItems)
	}
	return sandbox.Confirm(ctx, prompt, cmd.InOrStdin(), cmd.ErrOrStderr())
}

// printBrokenSandboxWarnings prints warnings for broken sandboxes
// (human-readable only).
func printBrokenSandboxWarnings(output interface{ Write([]byte) (int, error) }, brokenSandboxes []yoloai.BrokenSandbox, isJSON bool) {
	if isJSON {
		return
	}
	for _, bs := range brokenSandboxes {
		fmt.Fprintf(output, "Warning: broken sandbox at %s — use 'yoloai destroy %s' to remove\n", bs.Path, bs.Name) //nolint:errcheck
	}
}

// printPruneFoundItems reports what was found to prune
// (human-readable only).
func printPruneFoundItems(output interface{ Write([]byte) (int, error) }, items []yoloai.PruneItem, isJSON bool) {
	if isJSON {
		return
	}
	var orphans, temps []yoloai.PruneItem
	for _, item := range items {
		if item.Kind == "temp_dir" {
			temps = append(temps, item)
		} else {
			orphans = append(orphans, item)
		}
	}
	if len(orphans) > 0 {
		fmt.Fprintln(output, "Orphaned resources:") //nolint:errcheck
		for _, item := range orphans {
			fmt.Fprintf(output, "  %s %s\n", item.Kind, item.Name) //nolint:errcheck
		}
		fmt.Fprintln(output) //nolint:errcheck
	}
	if len(temps) > 0 {
		fmt.Fprintln(output, "Stale temporary files:") //nolint:errcheck
		for _, item := range temps {
			fmt.Fprintf(output, "  %s\n", item.Name) //nolint:errcheck
		}
		fmt.Fprintln(output) //nolint:errcheck
	}
}

// writePruneJSON outputs prune results as JSON.
func writePruneJSON(cmd *cobra.Command, result *yoloai.PruneResult, dryRun bool) error {
	type pruneItem struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	items := make([]pruneItem, 0, len(result.RemovedItems))
	for _, item := range result.RemovedItems {
		items = append(items, pruneItem{Kind: item.Kind, Name: item.Name})
	}
	return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
		"items":   items,
		"dry_run": dryRun,
	})
}
