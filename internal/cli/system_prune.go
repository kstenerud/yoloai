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
				return fmt.Errorf("--all and --backend are mutually exclusive")
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

	// 1. Scan sandbox directories to build known instances + broken sandbox list.
	knownInstances, brokenSandboxes := scanSandboxes()

	// 2. Scan for orphaned backend resources (always dry-run first).
	var scanResult runtime.PruneResult
	err := withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
		var err error
		scanResult, err = rt.Prune(ctx, knownInstances, true, output)
		return err
	})
	if err != nil {
		return err
	}

	// 3. Scan for stale temp files.
	staleTempDirs, err := sandbox.PruneTempFiles(true, 1*time.Hour)
	if err != nil {
		return fmt.Errorf("scan temp files: %w", err)
	}

	isJSON := jsonEnabled(cmd)

	// 4. Print broken sandbox warnings (human-readable only).
	if !isJSON {
		for _, bs := range brokenSandboxes {
			fmt.Fprintf(output, "Warning: broken sandbox at %s — use 'yoloai destroy %s' to remove\n", bs.path, bs.name) //nolint:errcheck
		}
	}

	// 5. Check if there's anything to prune.
	totalItems := len(scanResult.Items) + len(staleTempDirs)
	if totalItems == 0 {
		if isJSON {
			return writePruneJSON(cmd, scanResult, staleTempDirs, dryRun)
		}
		fmt.Fprintln(output, "Nothing to prune.") //nolint:errcheck
		return nil
	}

	// 6. Report what was found (human-readable only).
	if !isJSON {
		if len(scanResult.Items) > 0 {
			fmt.Fprintln(output, "Orphaned resources:") //nolint:errcheck
			for _, item := range scanResult.Items {
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

	// 7. If dry-run, stop here.
	if dryRun {
		if isJSON {
			return writePruneJSON(cmd, scanResult, staleTempDirs, true)
		}
		return nil
	}

	// 8. Confirm unless --yes.
	if !yes {
		prompt := fmt.Sprintf("Remove %d resource(s)? [y/N]: ", totalItems)
		confirmed, err := sandbox.Confirm(ctx, prompt, cmd.InOrStdin(), cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
	}

	// 9. Remove orphaned backend resources.
	if len(scanResult.Items) > 0 {
		err := withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
			_, err := rt.Prune(ctx, knownInstances, false, output)
			return err
		})
		if err != nil {
			return err
		}
		if !isJSON {
			for _, item := range scanResult.Items {
				fmt.Fprintf(output, "Removed %s %s\n", item.Kind, item.Name) //nolint:errcheck
			}
		}
	}

	// 10. Remove stale temp files.
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

// runSystemPruneAll prunes orphaned resources across all available backends.
func runSystemPruneAll(cmd *cobra.Command, dryRun, yes bool) error {
	ctx := cmd.Context()
	output := cmd.OutOrStdout()

	knownInstances, brokenSandboxes := scanSandboxes()

	// Scan all available backends, aggregating results.
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

	staleTempDirs, err := sandbox.PruneTempFiles(true, 1*time.Hour)
	if err != nil {
		return fmt.Errorf("scan temp files: %w", err)
	}

	isJSON := jsonEnabled(cmd)
	scanResult := runtime.PruneResult{Items: allItems}

	if !isJSON {
		for _, bs := range brokenSandboxes {
			fmt.Fprintf(output, "Warning: broken sandbox at %s — use 'yoloai destroy %s' to remove\n", bs.path, bs.name) //nolint:errcheck
		}
	}

	totalItems := len(allItems) + len(staleTempDirs)
	if totalItems == 0 {
		if isJSON {
			return writePruneJSON(cmd, scanResult, staleTempDirs, dryRun)
		}
		fmt.Fprintln(output, "Nothing to prune.") //nolint:errcheck
		return nil
	}

	if !isJSON {
		if len(allItems) > 0 {
			fmt.Fprintln(output, "Orphaned resources:") //nolint:errcheck
			for _, item := range allItems {
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

	if dryRun {
		if isJSON {
			return writePruneJSON(cmd, scanResult, staleTempDirs, true)
		}
		return nil
	}

	if !yes {
		prompt := fmt.Sprintf("Remove %d resource(s)? [y/N]: ", totalItems)
		confirmed, err := sandbox.Confirm(ctx, prompt, cmd.InOrStdin(), cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
	}

	// Remove orphaned resources from each available backend.
	if len(allItems) > 0 {
		for _, name := range availableBackends {
			err := withRuntime(ctx, name, func(ctx context.Context, rt runtime.Runtime) error {
				_, err := rt.Prune(ctx, knownInstances, false, output)
				return err
			})
			if err != nil {
				fmt.Fprintf(output, "Warning: prune %s failed: %v\n", name, err) //nolint:errcheck
			}
		}
		if !isJSON {
			for _, item := range allItems {
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
