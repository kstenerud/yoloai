package cli

// ABOUTME: `yoloai system prune` removes orphaned backend resources and stale temp files.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newSystemPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove orphaned backend resources and stale temp files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			yes := effectiveYes(cmd)
			all, _ := cmd.Flags().GetBool("all")
			backendFlag, _ := cmd.Flags().GetString("backend")

			if all && backendFlag != "" {
				return sandbox.NewUsageError("--all and --backend are mutually exclusive")
			}

			if all {
				return runSystemPruneAll(cmd, dryRun, yes)
			}

			return runSystemPrune(cmd, resolveBackend(cmd), dryRun, yes)
		},
	}

	cmd.Flags().Bool("dry-run", false, "Report only, don't remove anything")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")
	cmd.Flags().Bool("all", false, "Prune across all available backends")

	return cmd
}

func runSystemPrune(cmd *cobra.Command, backend string, dryRun, yes bool) error {
	ctx := cmd.Context()
	output := cmd.OutOrStdout()
	isJSON := jsonEnabled(cmd)

	knownInstances, brokenSandboxes := scanSandboxes()

	var scanResult runtime.PruneResult
	if err := withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
		var err error
		scanResult, err = rt.Prune(ctx, knownInstances, true, output)
		return err
	}); err != nil {
		return err
	}

	staleTempDirs, err := sandbox.PruneTempFiles(true, 1*time.Hour)
	if err != nil {
		return fmt.Errorf("scan temp files: %w", err)
	}

	printBrokenSandboxWarnings(output, brokenSandboxes, isJSON)

	totalItems := len(scanResult.Items) + len(staleTempDirs)
	if totalItems == 0 {
		if isJSON {
			return writePruneJSON(cmd, scanResult, staleTempDirs, dryRun)
		}
		fmt.Fprintln(output, "Nothing to prune.") //nolint:errcheck
		return nil
	}

	printPruneFoundItems(output, scanResult.Items, staleTempDirs, isJSON)

	if dryRun {
		if isJSON {
			return writePruneJSON(cmd, scanResult, staleTempDirs, true)
		}
		return nil
	}

	if !yes {
		confirmed, err := confirmPrune(cmd, ctx, totalItems)
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
	}

	return executePruneSingle(cmd, ctx, backend, knownInstances, scanResult, staleTempDirs, output, isJSON)
}

// confirmPrune prompts the user to confirm removal and returns whether they confirmed.
func confirmPrune(cmd *cobra.Command, ctx context.Context, totalItems int) (bool, error) {
	prompt := fmt.Sprintf("Remove %d resource(s)? [y/N]: ", totalItems)
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
func runSystemPruneAll(cmd *cobra.Command, dryRun, yes bool) error {
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
	if done, doneErr := pruneEmptyOrDryRun(cmd, output, scanResult, staleTempDirs, totalItems, dryRun, isJSON); done {
		return doneErr
	}

	if !yes {
		confirmed, err := confirmPrune(cmd, ctx, totalItems)
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
	}

	return executeAllPrune(cmd, ctx, availableBackends, knownInstances, scanResult, staleTempDirs, output, isJSON)
}

// pruneEmptyOrDryRun handles the "nothing to prune" and dry-run early exit cases.
// Returns (true, err) if the caller should return, (false, nil) to continue.
func pruneEmptyOrDryRun(cmd *cobra.Command, output interface{ Write([]byte) (int, error) }, scanResult runtime.PruneResult, staleTempDirs []string, totalItems int, dryRun, isJSON bool) (bool, error) {
	if totalItems == 0 {
		if isJSON {
			return true, writePruneJSON(cmd, scanResult, staleTempDirs, dryRun)
		}
		fmt.Fprintln(output, "Nothing to prune.") //nolint:errcheck
		return true, nil
	}

	printPruneFoundItems(output, scanResult.Items, staleTempDirs, isJSON)

	if dryRun {
		if isJSON {
			return true, writePruneJSON(cmd, scanResult, staleTempDirs, true)
		}
		return true, nil
	}
	return false, nil
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

	for _, b := range knownBackends {
		available, _ := checkBackend(ctx, b.Name)
		if !available {
			continue
		}
		availableBackends = append(availableBackends, b.Name)

		var result runtime.PruneResult
		err := withRuntime(ctx, b.Name, func(ctx context.Context, rt runtime.Runtime) error {
			var err error
			result, err = rt.Prune(ctx, knownInstances, true, output)
			return err
		})
		if err != nil {
			fmt.Fprintf(output, "Warning: scan %s failed: %v\n", b.Name, err) //nolint:errcheck
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
	sandboxesDir := config.SandboxesDir()
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

		if _, err := sandbox.LoadMeta(dir); err != nil {
			broken = append(broken, brokenSandbox{name: name, path: dir})
		} else {
			knownInstances = append(knownInstances, sandbox.InstanceName(name))
		}
	}

	return knownInstances, broken
}
