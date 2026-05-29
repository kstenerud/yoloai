package system

// ABOUTME: `yoloai system prune` removes orphaned backend resources, stale temp
// ABOUTME: files, never-init sandbox dirs, and orphaned lock files; quarantines
// ABOUTME: unclassifiable broken dirs to trash and refuses data-bearing ones.

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
only recoverable-safe cruft — sandbox-named containers/VMs with no matching
sandbox dir, stale yoloai temp dirs, never-initialized sandbox dirs, and
orphaned lock files. Broken sandbox dirs that still hold unreviewed work are
never touched (reported with a fix command); broken dirs that can't be
classified are quarantined to the trash dir, not deleted.

--cache also reclaims each backend's image cache, snapshots, volumes,
and build cache (forces yoloai-base to rebuild on next sandbox creation).
Use on a host dedicated to yoloai; on shared machines, prefer the
backend's own prune (e.g., 'docker system prune').`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			explicitYes, _ := cmd.Flags().GetBool("yes")
			cache, _ := cmd.Flags().GetBool("cache")
			return runSystemPrune(cmd, dryRun, explicitYes, cache)
		},
	}

	cmd.Flags().Bool("dry-run", false, "Report only, don't remove anything")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts (including trash deletion)")
	cmd.Flags().Bool("cache", false, "Also reclaim backend image cache + snapshots + build cache (DESTRUCTIVE: forces base rebuild)")

	return cmd
}

func runSystemPrune(cmd *cobra.Command, dryRun, explicitYes, cache bool) error {
	ctx := cmd.Context()
	output := cmd.OutOrStdout()
	isJSON := cliutil.JSONEnabled(cmd)
	// The main prune confirmation is skipped under --json too (EffectiveYes),
	// so non-interactive scripted runs don't hang. Trash deletion is gated on
	// the *explicit* --yes only — it may destroy data the user wanted, so
	// plain --json never empties trash on its own.
	skipPruneConfirm := cliutil.EffectiveYes(cmd)

	// First, a dry-run scan to find what's there.
	scanResult, err := cliutil.NewSystemClient().Prune(ctx, yoloai.PruneOptions{
		DryRun:           true,
		IncludeBaseImage: cache,
		Output:           output,
	})
	if err != nil {
		return err
	}

	proceed, err := previewPrune(cmd, scanResult, dryRun, cache, isJSON)
	if err != nil || !proceed {
		return err
	}

	totalItems := len(scanResult.RemovedItems)
	hasWork := totalItems > 0 || cache || len(scanResult.Trashed) > 0
	if hasWork && !skipPruneConfirm {
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
	printActualRemoval(output, actualResult, isJSON)

	// Offer to reclaim the trash dir (it may now include sandboxes just
	// quarantined plus anything from prior runs).
	if err := maybeEmptyTrash(cmd, ctx, actualResult.TrashContents, explicitYes, isJSON); err != nil {
		return err
	}

	if isJSON {
		return writePruneJSON(cmd, actualResult, false)
	}
	return nil
}

// previewPrune prints the dry-run preview (refused/trashed/found items,
// cache banner) and decides whether the caller should proceed to the
// actual removal. Returns proceed=false when there's nothing to do or when
// this was itself a --dry-run invocation.
func previewPrune(cmd *cobra.Command, scan *yoloai.PruneResult, dryRun, cache, isJSON bool) (bool, error) {
	output := cmd.OutOrStdout()
	printRefusedDataBearing(output, scan.RefusedDataBearing, isJSON)
	printTrashedPreview(output, scan.Trashed, isJSON)

	totalItems := len(scan.RemovedItems)
	hasWork := totalItems > 0 || cache || len(scan.Trashed) > 0
	if !hasWork && scan.TrashContents.Count == 0 {
		if isJSON {
			return false, writePruneJSON(cmd, scan, true)
		}
		if len(scan.RefusedDataBearing) == 0 {
			fmt.Fprintln(output, "Nothing to prune.")                                                           //nolint:errcheck
			fmt.Fprintln(output, "(For stuck containers, leftover netns, or stale state: yoloai help cleanup)") //nolint:errcheck
		}
		return false, nil
	}

	if totalItems > 0 {
		printPruneFoundItems(output, scan.RemovedItems, isJSON)
	}
	announceCache(output, cache, isJSON)

	if dryRun {
		printTrashStatus(output, scan.TrashContents, isJSON)
		if isJSON {
			return false, writePruneJSON(cmd, scan, true)
		}
		return false, nil
	}
	return true, nil
}

// printActualRemoval reports what the non-dry-run prune actually removed
// and quarantined (human-mode only).
func printActualRemoval(output interface{ Write([]byte) (int, error) }, result *yoloai.PruneResult, isJSON bool) {
	if isJSON {
		return
	}
	for _, item := range result.RemovedItems {
		switch item.Kind {
		case yoloai.PruneKindTempDir:
			fmt.Fprintf(output, "Removed temp dir %s\n", item.Name) //nolint:errcheck
		case yoloai.PruneKindLockFile:
			fmt.Fprintf(output, "Removed orphaned lock for %s\n", item.Name) //nolint:errcheck
		case yoloai.PruneKindSandboxDir:
			fmt.Fprintf(output, "Removed never-initialized sandbox %s\n", item.Name) //nolint:errcheck
		default:
			fmt.Fprintf(output, "Removed %s %s\n", item.Kind, item.Name) //nolint:errcheck
		}
	}
	for _, t := range result.Trashed {
		fmt.Fprintf(output, "Quarantined broken sandbox %s to trash (%s)\n", t.Name, t.Dest) //nolint:errcheck
	}
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

// printRefusedDataBearing warns about broken sandbox dirs that still hold
// unreviewed work — prune leaves them alone; the user must act.
func printRefusedDataBearing(output interface{ Write([]byte) (int, error) }, refused []yoloai.RefusedSandbox, isJSON bool) {
	if isJSON || len(refused) == 0 {
		return
	}
	fmt.Fprintln(output, "Broken sandboxes holding unreviewed work (left untouched):") //nolint:errcheck
	for _, r := range refused {
		fmt.Fprintf(output, "  %s — %s\n", r.Name, r.Detail)                                             //nolint:errcheck
		fmt.Fprintf(output, "    review: yoloai diff %s    remove: yoloai destroy %s\n", r.Name, r.Name) //nolint:errcheck
	}
	fmt.Fprintln(output) //nolint:errcheck
}

// printTrashedPreview reports broken dirs that will be quarantined to trash
// (dry-run preview — Dest is not yet populated).
func printTrashedPreview(output interface{ Write([]byte) (int, error) }, trashed []yoloai.TrashedSandbox, isJSON bool) {
	if isJSON || len(trashed) == 0 {
		return
	}
	fmt.Fprintln(output, "Broken sandboxes to quarantine to trash (recoverable with mv):") //nolint:errcheck
	for _, t := range trashed {
		fmt.Fprintf(output, "  %s — %s\n", t.Name, t.Reason) //nolint:errcheck
	}
	fmt.Fprintln(output) //nolint:errcheck
}

// printTrashStatus reports the current trash dir contents (count + size)
// and how to recover or reclaim it.
func printTrashStatus(output interface{ Write([]byte) (int, error) }, trash yoloai.TrashSummary, isJSON bool) {
	if isJSON || trash.Count == 0 {
		return
	}
	msg := fmt.Sprintf("Trash holds %d item(s) (%s) — recover with mv, or reclaim by running prune without --dry-run.\n",
		trash.Count, cliutil.HumanBytes(trash.Bytes))
	fmt.Fprint(output, msg) //nolint:errcheck
}

// maybeEmptyTrash offers to delete the trash dir. Trash may hold data the
// user wanted, so deletion is conservative: prompt by default (no/EOF →
// keep), delete without prompting only on explicit --yes. In JSON mode it
// never prompts (would corrupt output) — it keeps trash unless --yes.
func maybeEmptyTrash(cmd *cobra.Command, ctx context.Context, trash yoloai.TrashSummary, explicitYes, isJSON bool) error {
	if trash.Count == 0 {
		return nil
	}
	output := cmd.OutOrStdout()

	if !explicitYes {
		if isJSON {
			return nil // keep trash; JSON consumers re-run with --yes to reclaim
		}
		prompt := fmt.Sprintf(
			"Trash holds %d item(s) (%s) that may contain data you wanted — delete it? [y/N]: ",
			trash.Count, cliutil.HumanBytes(trash.Bytes))
		confirmed, err := sandbox.Confirm(ctx, prompt, cmd.InOrStdin(), cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		if !confirmed {
			msg := fmt.Sprintf("Trash kept (%d item(s)). Recover with mv, or delete later with 'yoloai system prune --yes'.\n", trash.Count)
			fmt.Fprint(output, msg) //nolint:errcheck
			return nil
		}
	}

	removed, freed, err := cliutil.NewSystemClient().EmptyTrash()
	if err != nil {
		return err
	}
	if !isJSON {
		fmt.Fprintf(output, "Emptied trash: removed %d item(s), freed %s.\n", removed, cliutil.HumanBytes(freed)) //nolint:errcheck
	}
	return nil
}

// printPruneFoundItems reports what was found to prune
// (human-readable only).
func printPruneFoundItems(output interface{ Write([]byte) (int, error) }, items []yoloai.PruneItem, isJSON bool) {
	if isJSON {
		return
	}
	var orphans, temps, hostCruft []yoloai.PruneItem
	for _, item := range items {
		switch item.Kind {
		case yoloai.PruneKindTempDir:
			temps = append(temps, item)
		case yoloai.PruneKindLockFile, yoloai.PruneKindSandboxDir:
			hostCruft = append(hostCruft, item)
		default:
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
	if len(hostCruft) > 0 {
		fmt.Fprintln(output, "Leftover yoloai state:") //nolint:errcheck
		for _, item := range hostCruft {
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
	type refusedItem struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Detail string `json:"detail"`
	}
	type trashedItem struct {
		Name   string `json:"name"`
		Dest   string `json:"dest"`
		Reason string `json:"reason"`
	}
	items := make([]pruneItem, 0, len(result.RemovedItems))
	for _, item := range result.RemovedItems {
		items = append(items, pruneItem{Kind: string(item.Kind), Name: item.Name})
	}
	refused := make([]refusedItem, 0, len(result.RefusedDataBearing))
	for _, r := range result.RefusedDataBearing {
		refused = append(refused, refusedItem{Name: r.Name, Path: r.Path, Detail: r.Detail})
	}
	trashed := make([]trashedItem, 0, len(result.Trashed))
	for _, t := range result.Trashed {
		trashed = append(trashed, trashedItem{Name: t.Name, Dest: t.Dest, Reason: t.Reason})
	}
	return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
		"items":       items,
		"refused":     refused,
		"trashed":     trashed,
		"trash_count": result.TrashContents.Count,
		"trash_bytes": result.TrashContents.Bytes,
		"dry_run":     dryRun,
	})
}
