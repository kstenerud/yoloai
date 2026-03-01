package cli

// ABOUTME: `yoloai system prune` removes orphaned backend resources and stale temp files.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSystemPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove orphaned backend resources and stale temp files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			yes, _ := cmd.Flags().GetBool("yes")
			backend := resolveBackend(cmd)

			return runSystemPrune(cmd, backend, dryRun, yes)
		},
	}

	cmd.Flags().Bool("dry-run", false, "Report only, don't remove anything")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")

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
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}

	sandboxesDir := filepath.Join(homeDir, ".yoloai", "sandboxes")
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
