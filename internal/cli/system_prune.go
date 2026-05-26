package cli

// ABOUTME: `yoloai system prune` removes orphaned backend resources and stale temp files.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/store"
	"github.com/spf13/cobra"
)

func newSystemPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove orphaned backend resources and stale temp files",
		Long: `Remove orphaned backend resources and stale temp files.

By default removes only orphans (sandbox-named containers/VMs with no matching
sandbox dir on the host) and stale yoloai temp dirs — safe to run anytime.

--cache also reclaims the backend's image cache, snapshots, volumes, and build
cache (forces yoloai-base to rebuild on next sandbox creation). Use on a host
dedicated to yoloai; on shared machines, prefer the backend's own prune
(e.g., 'docker system prune').`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			yes := effectiveYes(cmd)
			all, _ := cmd.Flags().GetBool("all")
			cache, _ := cmd.Flags().GetBool("cache")
			backendFlag, _ := cmd.Flags().GetString("backend")

			if all && backendFlag != "" {
				return sandbox.NewUsageError("--all and --backend are mutually exclusive")
			}

			if all {
				return runSystemPruneAll(cmd, dryRun, yes, cache)
			}

			return runSystemPrune(cmd, resolveBackend(cmd), dryRun, yes, cache)
		},
	}

	cmd.Flags().Bool("dry-run", false, "Report only, don't remove anything")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")
	cmd.Flags().Bool("all", false, "Prune across all available backends")
	cmd.Flags().Bool("cache", false, "Also reclaim backend image cache + snapshots + build cache (DESTRUCTIVE: forces base rebuild)")

	return cmd
}

func runSystemPrune(cmd *cobra.Command, backend string, dryRun, yes, cache bool) error {
	ctx := cmd.Context()
	output := cmd.OutOrStdout()
	isJSON := jsonEnabled(cmd)

	knownInstances, brokenSandboxes := scanSandboxes()

	scanResult, staleTempDirs, err := scanSingleBackend(ctx, backend, knownInstances, output)
	if err != nil {
		return err
	}

	printBrokenSandboxWarnings(output, brokenSandboxes, isJSON)

	totalItems := len(scanResult.Items) + len(staleTempDirs)
	if done := reportPruneEarly(cmd, output, scanResult, staleTempDirs, totalItems, dryRun, cache, isJSON); done {
		return nil
	}
	announceCache(output, cache, isJSON, "Cache reclaim requested for backend: "+backend)

	if dryRun {
		return finishDryRun(cmd, ctx, []string{backend}, scanResult, staleTempDirs, output, cache, isJSON)
	}
	if !yes {
		confirmed, err := confirmPrune(cmd, ctx, totalItems, cache)
		if err != nil || !confirmed {
			return err
		}
	}

	if err := executePruneSingle(cmd, ctx, backend, knownInstances, scanResult, staleTempDirs, output, isJSON); err != nil {
		return err
	}
	if cache {
		return withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
			return runtime.PruneCacheFor(ctx, rt, false, output)
		})
	}
	return nil
}

// scanSingleBackend runs the dry-run prune scan for one backend plus the stale
// temp-dir scan, returning both for the caller to report on.
func scanSingleBackend(ctx context.Context, backend string, knownInstances []string, output interface{ Write([]byte) (int, error) }) (runtime.PruneResult, []string, error) {
	var scanResult runtime.PruneResult
	if err := withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
		var err error
		scanResult, err = rt.Prune(ctx, knownInstances, true, output)
		return err
	}); err != nil {
		return runtime.PruneResult{}, nil, err
	}
	staleTempDirs, err := sandbox.PruneTempFiles(true, 1*time.Hour)
	if err != nil {
		return runtime.PruneResult{}, nil, fmt.Errorf("scan temp files: %w", err)
	}
	return scanResult, staleTempDirs, nil
}

// reportPruneEarly handles the "nothing to do" exit and the orphan-list print.
// Returns true if the caller should return immediately (nothing left to do).
func reportPruneEarly(cmd *cobra.Command, output interface{ Write([]byte) (int, error) }, scanResult runtime.PruneResult, staleTempDirs []string, totalItems int, dryRun, cache, isJSON bool) bool {
	// Cache reclaim runs even with zero orphans — accumulated images/snapshots
	// are the usual reason a user invokes --cache.
	if totalItems == 0 && !cache {
		if isJSON {
			_ = writePruneJSON(cmd, scanResult, staleTempDirs, dryRun)
		} else {
			fmt.Fprintln(output, "Nothing to prune.") //nolint:errcheck
		}
		return true
	}
	if totalItems > 0 {
		printPruneFoundItems(output, scanResult.Items, staleTempDirs, isJSON)
	}
	return false
}

// announceCache prints the cache-reclaim banner (human-mode only).
func announceCache(output interface{ Write([]byte) (int, error) }, cache, isJSON bool, header string) {
	if !cache || isJSON {
		return
	}
	fmt.Fprintln(output, header)                                                   //nolint:errcheck
	fmt.Fprintln(output, "  (forces base image rebuild on next sandbox creation)") //nolint:errcheck
}

// finishDryRun runs the cache prune in dry-run mode (if requested) and writes
// the JSON report. Used by both single-backend and all-backends paths.
func finishDryRun(cmd *cobra.Command, ctx context.Context, backends []string, scanResult runtime.PruneResult, staleTempDirs []string, output interface{ Write([]byte) (int, error) }, cache, isJSON bool) error {
	if cache {
		pruneCacheAll(ctx, backends, true, output)
	}
	if isJSON {
		return writePruneJSON(cmd, scanResult, staleTempDirs, true)
	}
	return nil
}

// confirmPrune prompts the user to confirm removal and returns whether they confirmed.
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

// executePruneSingle carries out the actual removal for a single backend.
func executePruneSingle(cmd *cobra.Command, ctx context.Context, backend string, knownInstances []string, scanResult runtime.PruneResult, staleTempDirs []string, output interface{ Write([]byte) (int, error) }, isJSON bool) error {
	if len(scanResult.Items) > 0 {
		var actualResult runtime.PruneResult
		if err := withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
			var err error
			actualResult, err = rt.Prune(ctx, knownInstances, false, output)
			return err
		}); err != nil {
			return err
		}
		if !isJSON {
			for _, item := range actualResult.Items {
				fmt.Fprintf(output, "Removed %s %s\n", item.Kind, item.Name) //nolint:errcheck
			}
		}
	}

	if _, err := sandbox.PruneTempFiles(false, 1*time.Hour); err != nil {
		return fmt.Errorf("remove temp files: %w", err)
	}
	if !isJSON {
		for _, path := range staleTempDirs {
			fmt.Fprintf(output, "Removed temp dir %s\n", path) //nolint:errcheck
		}
	}

	if isJSON {
		return writePruneJSON(cmd, scanResult, staleTempDirs, false)
	}
	return nil
}

// printBrokenSandboxWarnings prints warnings for broken sandboxes (human-readable only).
func printBrokenSandboxWarnings(output interface{ Write([]byte) (int, error) }, brokenSandboxes []brokenSandbox, isJSON bool) {
	if isJSON {
		return
	}
	for _, bs := range brokenSandboxes {
		fmt.Fprintf(output, "Warning: broken sandbox at %s — use 'yoloai destroy %s' to remove\n", bs.path, bs.name) //nolint:errcheck
	}
}

// printPruneFoundItems reports what was found to prune (human-readable only).
func printPruneFoundItems(output interface{ Write([]byte) (int, error) }, items []runtime.PruneItem, staleTempDirs []string, isJSON bool) {
	if isJSON {
		return
	}
	if len(items) > 0 {
		fmt.Fprintln(output, "Orphaned resources:") //nolint:errcheck
		for _, item := range items {
			fmt.Fprintf(output, "  %s %s\n", item.Kind, item.Name) //nolint:errcheck
		}
		fmt.Fprintln(output) //nolint:errcheck
	}
	if len(staleTempDirs) > 0 {
		fmt.Fprintln(output, "Stale temporary files:") //nolint:errcheck
		for _, path := range staleTempDirs {
			fmt.Fprintf(output, "  %s\n", path) //nolint:errcheck
		}
		fmt.Fprintln(output) //nolint:errcheck
	}
}

// runSystemPruneAll prunes orphaned resources across all available backends.
func runSystemPruneAll(cmd *cobra.Command, dryRun, yes, cache bool) error {
	ctx := cmd.Context()
	output := cmd.OutOrStdout()
	isJSON := jsonEnabled(cmd)

	knownInstances, brokenSandboxes := scanSandboxes()
	allItems, availableBackends := scanAllBackendsForPrune(ctx, knownInstances, output)

	staleTempDirs, err := sandbox.PruneTempFiles(true, 1*time.Hour)
	if err != nil {
		return fmt.Errorf("scan temp files: %w", err)
	}

	scanResult := runtime.PruneResult{Items: allItems}
	printBrokenSandboxWarnings(output, brokenSandboxes, isJSON)

	totalItems := len(allItems) + len(staleTempDirs)
	if done := reportPruneEarly(cmd, output, scanResult, staleTempDirs, totalItems, dryRun, cache, isJSON); done {
		return nil
	}
	announceCache(output, cache, isJSON, fmt.Sprintf("Cache reclaim requested across: %v", availableBackends))

	if dryRun {
		return finishDryRun(cmd, ctx, availableBackends, scanResult, staleTempDirs, output, cache, isJSON)
	}
	if !yes {
		confirmed, err := confirmPrune(cmd, ctx, totalItems, cache)
		if err != nil || !confirmed {
			return err
		}
	}

	if err := executeAllPrune(cmd, ctx, availableBackends, knownInstances, scanResult, staleTempDirs, output, isJSON); err != nil {
		return err
	}
	if cache {
		pruneCacheAll(ctx, availableBackends, false, output)
	}
	return nil
}

// pruneCacheAll runs PruneCache on every named backend, logging per-backend
// failures rather than aborting (one backend's cache prune shouldn't block
// the others).
func pruneCacheAll(ctx context.Context, backends []string, dryRun bool, output interface{ Write([]byte) (int, error) }) {
	for _, name := range backends {
		err := withRuntime(ctx, name, func(ctx context.Context, rt runtime.Runtime) error {
			return runtime.PruneCacheFor(ctx, rt, dryRun, output)
		})
		if err != nil {
			fmt.Fprintf(output, "Warning: cache prune %s failed: %v\n", name, err) //nolint:errcheck
		}
	}
}

// executeAllPrune carries out the actual removal across all backends.
func executeAllPrune(cmd *cobra.Command, _ context.Context, availableBackends, knownInstances []string, scanResult runtime.PruneResult, staleTempDirs []string, output interface{ Write([]byte) (int, error) }, isJSON bool) error {
	if len(scanResult.Items) > 0 {
		ctx := cmd.Context()
		actualItems := executeAllBackendsPrune(ctx, availableBackends, knownInstances, output)
		if !isJSON {
			for _, item := range actualItems {
				fmt.Fprintf(output, "Removed %s %s\n", item.Kind, item.Name) //nolint:errcheck
			}
		}
	}

	if _, err := sandbox.PruneTempFiles(false, 1*time.Hour); err != nil {
		return fmt.Errorf("remove temp files: %w", err)
	}
	if !isJSON {
		for _, path := range staleTempDirs {
			fmt.Fprintf(output, "Removed temp dir %s\n", path) //nolint:errcheck
		}
	}

	if isJSON {
		return writePruneJSON(cmd, scanResult, staleTempDirs, false)
	}
	return nil
}

// scanAllBackendsForPrune scans all available backends for orphaned resources.
func scanAllBackendsForPrune(ctx context.Context, knownInstances []string, output interface{ Write([]byte) (int, error) }) ([]runtime.PruneItem, []string) {
	var allItems []runtime.PruneItem
	var availableBackends []string

	for _, desc := range runtime.Descriptors() {
		available, _ := checkBackend(ctx, desc.Name)
		if !available {
			continue
		}
		availableBackends = append(availableBackends, desc.Name)

		var result runtime.PruneResult
		err := withRuntime(ctx, desc.Name, func(ctx context.Context, rt runtime.Runtime) error {
			var err error
			result, err = rt.Prune(ctx, knownInstances, true, output)
			return err
		})
		if err != nil {
			fmt.Fprintf(output, "Warning: scan %s failed: %v\n", desc.Name, err) //nolint:errcheck
			continue
		}
		allItems = append(allItems, result.Items...)
	}

	return allItems, availableBackends
}

// executeAllBackendsPrune removes orphaned resources from each available backend.
func executeAllBackendsPrune(ctx context.Context, availableBackends, knownInstances []string, output interface{ Write([]byte) (int, error) }) []runtime.PruneItem {
	var actualItems []runtime.PruneItem
	for _, name := range availableBackends {
		err := withRuntime(ctx, name, func(ctx context.Context, rt runtime.Runtime) error {
			actual, err := rt.Prune(ctx, knownInstances, false, output)
			if err == nil {
				actualItems = append(actualItems, actual.Items...)
			}
			return err
		})
		if err != nil {
			fmt.Fprintf(output, "Warning: prune %s failed: %v\n", name, err) //nolint:errcheck
		}
	}
	return actualItems
}

// writePruneJSON outputs prune results as JSON.
func writePruneJSON(cmd *cobra.Command, scanResult runtime.PruneResult, staleTempDirs []string, dryRun bool) error {
	type pruneItem struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}

	items := make([]pruneItem, 0, len(scanResult.Items)+len(staleTempDirs))
	for _, item := range scanResult.Items {
		items = append(items, pruneItem{Kind: item.Kind, Name: item.Name})
	}
	for _, path := range staleTempDirs {
		items = append(items, pruneItem{Kind: "temp_dir", Name: path})
	}

	return writeJSON(cmd.OutOrStdout(), map[string]any{
		"items":   items,
		"dry_run": dryRun,
	})
}

type brokenSandbox struct {
	name string
	path string
}

// scanSandboxes reads ~/.yoloai/sandboxes/ and classifies each entry.
func scanSandboxes() (knownInstances []string, broken []brokenSandbox) {
	sandboxesDir := cliLayout().SandboxesDir()
	entries, err := os.ReadDir(sandboxesDir)
	if err != nil {
		return nil, nil // directory might not exist yet
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dir := filepath.Join(sandboxesDir, name)

		if _, err := store.LoadMeta(dir); err != nil {
			broken = append(broken, brokenSandbox{name: name, path: dir})
		} else {
			knownInstances = append(knownInstances, store.InstanceName(name))
		}
	}

	return knownInstances, broken
}
