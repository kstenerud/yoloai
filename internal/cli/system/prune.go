package system

// ABOUTME: `yoloai system prune` removes orphaned backend resources, stale temp
// ABOUTME: files, never-init sandbox dirs, and orphaned lock files; quarantines
// ABOUTME: unclassifiable broken dirs to trash and refuses data-bearing ones.

import (
	"context"
	"fmt"
	"io"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
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

Reclaims each backend's no-rebuild cache — this never forces a rebuild, so
'new' still runs without rebuilding afterward. yoloai's own stopped containers
and volumes are removed by label, so a shared daemon's foreign content is left
alone; the build cache and dangling images have no per-project attribution, so
that reclaim is daemon-wide.

--images additionally removes each backend's base/profile images, which
forces yoloai-base to rebuild on the next sandbox creation. On docker and
podman only yoloai's own images are removed (matched by the com.yoloai.managed
label, or a yoloai- name for images from builds that predate the label);
foreign images on a shared daemon are left alone. The apple backend cannot
filter images yet, so its --images remains daemon-wide.

--stale-bases removes superseded base images a backend left behind — e.g. an
old-macOS Tart base orphaned when the host OS (and the resolved base codename)
changed. It never touches the current base, so it forces no rebuild.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			images, _ := cmd.Flags().GetBool("images")
			staleBases, _ := cmd.Flags().GetBool("stale-bases")
			trash, _ := cmd.Flags().GetBool("trash")
			return runSystemPrune(cmd, dryRun, images, staleBases, trash)
		},
	}

	cmd.Flags().Bool("dry-run", false, "Report only, don't remove anything")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts")
	cmd.Flags().Bool("images", false, "Also remove backend base/profile images (DESTRUCTIVE: forces base rebuild)")
	cmd.Flags().Bool("stale-bases", false, "Also remove superseded base images left by a host-OS change (no rebuild forced)")
	cmd.Flags().Bool("trash", false, "Also empty the trash dir (deletes quarantined broken sandboxes, including any quarantined by this run)")

	return cmd
}

func runSystemPrune(cmd *cobra.Command, dryRun, images, staleBases, trash bool) error {
	ctx := cmd.Context()
	output := cmd.OutOrStdout()
	isJSON := cliutil.JSONEnabled(cmd)
	// The prune confirmation (Class A — it confirms the reclaim you invoked) is
	// skipped under --json too, so scripted runs don't hang. Emptying the trash
	// widens the destructive scope and is opt-in via --trash alone; --yes only
	// suppresses the confirmation prompt, it never authorizes the wider scope.
	skipPruneConfirm := cliutil.EffectiveYes(cmd)

	sys, err := cliutil.System()
	if err != nil {
		return err
	}

	// First, a dry-run scan to find what's there.
	scanResult, err := sys.Prune(ctx, yoloai.SystemPruneOptions{
		DryRun:            true,
		IncludeBaseImage:  images,
		IncludeStaleBases: staleBases,
		Output:            output,
	})
	if err != nil {
		return err
	}

	proceed, err := previewPrune(cmd, scanResult, dryRun, images, isJSON)
	if err != nil || !proceed {
		return err
	}

	totalItems := len(scanResult.RemovedItems)
	hasWork := totalItems > 0 || scanResult.FreedBytes > 0 || len(scanResult.Trashed) > 0
	if hasWork && !skipPruneConfirm {
		confirmed, err := confirmPrune(cmd, ctx, totalItems, images)
		if err != nil || !confirmed {
			return err
		}
	}

	// Actual removal. The library does the work; we just report.
	actualResult, err := sys.Prune(ctx, yoloai.SystemPruneOptions{
		DryRun:            false,
		IncludeBaseImage:  images,
		IncludeStaleBases: staleBases,
		Output:            output,
	})
	if err != nil {
		return err
	}
	printActualRemoval(output, actualResult, images, isJSON)

	// The trash dir (sandboxes just quarantined plus anything from prior runs)
	// holds recoverable data, so emptying it is opt-in via --trash. Without it
	// we only report what's there; with it we empty (prompt suppressed by --yes).
	if trash {
		if err := emptyTrash(cmd, ctx, actualResult.TrashContents, skipPruneConfirm, isJSON); err != nil {
			return err
		}
	} else {
		printTrashStatus(output, actualResult.TrashContents, isJSON)
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
func previewPrune(cmd *cobra.Command, scan *yoloai.PruneResult, dryRun, images, isJSON bool) (bool, error) {
	output := cmd.OutOrStdout()
	printRefusedDataBearing(output, scan.RefusedDataBearing, isJSON)
	printTrashedPreview(output, scan.Trashed, isJSON)

	totalItems := len(scan.RemovedItems)
	hasWork := totalItems > 0 || scan.FreedBytes > 0 || len(scan.Trashed) > 0
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
	announceReclaim(output, scan.FreedBytes, images, isJSON)

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
func printActualRemoval(output io.Writer, result *yoloai.PruneResult, images, isJSON bool) {
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
		case yoloai.PruneKindStaleBase:
			fmt.Fprintf(output, "Removed superseded base image %s\n", item.Name) //nolint:errcheck
		default:
			fmt.Fprintf(output, "Removed %s %s\n", item.Kind, item.Name) //nolint:errcheck
		}
	}
	for _, t := range result.Trashed {
		fmt.Fprintf(output, "Quarantined broken sandbox %s to trash (%s)\n", t.Name, t.TrashPath) //nolint:errcheck
	}
	if result.FreedBytes > 0 {
		what := "backend cache"
		if images {
			what = "backend cache + base images"
		}
		fmt.Fprintf(output, "Reclaimed %s of %s.\n", cliutil.HumanBytes(result.FreedBytes), what) //nolint:errcheck
	}
}

// announceReclaim previews the cache that will be reclaimed (best-effort
// estimate) and, when --images is set, warns that base images will be removed
// (forcing a rebuild). Human-mode only.
func announceReclaim(output io.Writer, reclaimBytes int64, images, isJSON bool) {
	if isJSON {
		return
	}
	if reclaimBytes > 0 {
		label := "Reclaimable backend cache (no rebuild)"
		if images {
			label = "Reclaimable backend cache + base images"
		}
		fmt.Fprintf(output, "%s: ~%s\n", label, cliutil.HumanBytes(reclaimBytes)) //nolint:errcheck
	}
	if images {
		fmt.Fprintln(output, "--images: also removing base images (forces yoloai-base rebuild on next 'new')") //nolint:errcheck
	}
}

// confirmPrune prompts the user to confirm removal and returns
// whether they confirmed.
func confirmPrune(cmd *cobra.Command, ctx context.Context, totalItems int, images bool) (bool, error) {
	var prompt string
	switch {
	case totalItems == 0 && images:
		prompt = "Reclaim cache and remove base images (rebuilds yoloai-base on next 'new')? [y/N]: "
	case images:
		prompt = fmt.Sprintf("Remove %d resource(s), reclaim cache, and remove base images (rebuilds yoloai-base on next 'new')? [y/N]: ", totalItems)
	case totalItems == 0:
		prompt = "Reclaim backend cache? [y/N]: "
	default:
		prompt = fmt.Sprintf("Remove %d resource(s) and reclaim cache? [y/N]: ", totalItems)
	}
	return cliutil.Confirm(ctx, prompt, cmd.InOrStdin(), cmd.ErrOrStderr())
}

// printRefusedDataBearing warns about broken sandbox dirs that still hold
// unreviewed work — prune leaves them alone; the user must act.
func printRefusedDataBearing(output io.Writer, refused []yoloai.RefusedSandbox, isJSON bool) {
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
func printTrashedPreview(output io.Writer, trashed []yoloai.TrashedSandbox, isJSON bool) {
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
func printTrashStatus(output io.Writer, trash yoloai.TrashSummary, isJSON bool) {
	if isJSON || trash.Count == 0 {
		return
	}
	msg := fmt.Sprintf("Trash holds %d item(s) (%s) — recover with mv, or empty with 'yoloai system prune --trash'.\n",
		trash.Count, cliutil.HumanBytes(trash.Bytes))
	fmt.Fprint(output, msg) //nolint:errcheck
}

// emptyTrash deletes the trash dir's contents. It is only reached when --trash
// was passed — the explicit opt-in to widen the scope to recoverable data. The
// prompt here merely confirms the already-selected action and is suppressed by
// skipConfirm (--yes/--json); it never widens scope on its own. In JSON mode
// skipConfirm is true (EffectiveYes), so it empties without prompting.
func emptyTrash(cmd *cobra.Command, ctx context.Context, trash yoloai.TrashSummary, skipConfirm, isJSON bool) error {
	output := cmd.OutOrStdout()
	if trash.Count == 0 {
		if !isJSON {
			fmt.Fprintln(output, "Trash is already empty.") //nolint:errcheck
		}
		return nil
	}

	if !skipConfirm {
		prompt := fmt.Sprintf(
			"Delete %d trash item(s) (%s)? This cannot be undone. [y/N]: ",
			trash.Count, cliutil.HumanBytes(trash.Bytes))
		confirmed, err := cliutil.Confirm(ctx, prompt, cmd.InOrStdin(), cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintf(output, "Trash kept (%d item(s)). Recover with mv, or empty later with 'yoloai system prune --trash'.\n", trash.Count) //nolint:errcheck
			return nil
		}
	}

	sys, err := cliutil.System()
	if err != nil {
		return err
	}
	removed, freed, err := sys.EmptyTrash()
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
func printPruneFoundItems(output io.Writer, items []yoloai.PruneItem, isJSON bool) {
	if isJSON {
		return
	}
	var orphans, temps, hostCruft, staleBases []yoloai.PruneItem
	for _, item := range items {
		switch item.Kind {
		case yoloai.PruneKindTempDir:
			temps = append(temps, item)
		case yoloai.PruneKindLockFile, yoloai.PruneKindSandboxDir:
			hostCruft = append(hostCruft, item)
		case yoloai.PruneKindStaleBase:
			staleBases = append(staleBases, item)
		default:
			orphans = append(orphans, item)
		}
	}
	if len(staleBases) > 0 {
		fmt.Fprintln(output, "Superseded base images:") //nolint:errcheck
		for _, item := range staleBases {
			fmt.Fprintf(output, "  %s\n", item.Name) //nolint:errcheck
		}
		fmt.Fprintln(output) //nolint:errcheck
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
		trashed = append(trashed, trashedItem{Name: t.Name, Dest: t.TrashPath, Reason: t.Reason})
	}
	return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
		"items":       items,
		"refused":     refused,
		"trashed":     trashed,
		"freed_bytes": result.FreedBytes,
		"trash_count": result.TrashContents.Count,
		"trash_bytes": result.TrashContents.Bytes,
		"dry_run":     dryRun,
	})
}
